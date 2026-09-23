//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/media"
	"github.com/onerandomd3v/signa/internal/reports"
)

type fakeMediaStorage struct {
	mu         sync.Mutex
	objects    map[string]media.ObjectMetadata
	presignErr error
	headErr    error
	headCalls  int
}

func (s *fakeMediaStorage) PresignPut(_ context.Context, objectKey, contentType string, expires time.Duration) (string, map[string]string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.presignErr != nil {
		return "", nil, time.Time{}, s.presignErr
	}
	return "https://uploads.test/" + objectKey, map[string]string{"Content-Type": contentType}, time.Now().UTC().Add(expires), nil
}

func (s *fakeMediaStorage) Head(_ context.Context, objectKey string) (media.ObjectMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.headCalls++
	if s.headErr != nil {
		return media.ObjectMetadata{}, s.headErr
	}
	object, ok := s.objects[objectKey]
	if !ok {
		return media.ObjectMetadata{}, media.ErrObjectNotFound
	}
	return object, nil
}

func (s *fakeMediaStorage) setObject(objectKey string, object media.ObjectMetadata) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[objectKey] = object
}

func (s *fakeMediaStorage) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.headCalls
}

func TestMediaHTTPIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}
	testURL, maintenance, databaseName := createMediaIntegrationDatabase(t, ctx, databaseURL)
	defer func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+databaseName)
		_ = maintenance.Close(context.Background())
	}()
	runMediaGoose(t, ctx, testURL, "up")

	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping pool: %v", err)
	}

	storage := &fakeMediaStorage{objects: map[string]media.ObjectMetadata{}}
	store := reports.NewStore(pool)
	acknowledgement, err := store.Ingest(ctx, "media-http-report", reports.IngestRequest{RawText: "Media integration report"})
	if err != nil {
		t.Fatalf("create report: %v", err)
	}
	reportID := acknowledgement.ReportID
	rateConfig := RateLimitConfig{PerClientRatePerMinute: 1000, PerClientBurst: 1000, GlobalRatePerMinute: 1000, GlobalBurst: 1000}
	handler := NewHandlerWithMedia(nil, rateConfig, pool, storage)

	t.Run("upload authorization and validation", func(t *testing.T) {
		response := mediaRequest(t, handler, http.MethodPost, "/reports/"+reportID+"/media/uploads", mediaUploadRequest{MediaType: media.TypeImage, ContentType: "image/png", SizeBytes: 1024}, "10.0.0.1:1000")
		if response.Code != http.StatusOK {
			t.Fatalf("authorization status = %d, want 200", response.Code)
		}
		var authorization uploadAuthorization
		decodeMediaResponse(t, response, &authorization)
		if !media.IsObjectKeyForReport(authorization.ObjectKey, reportID) || authorization.UploadURL == "" {
			t.Fatalf("authorization = %+v", authorization)
		}

		missing := mediaRequest(t, handler, http.MethodPost, "/reports/"+uuid.NewString()+"/media/uploads", mediaUploadRequest{MediaType: media.TypeImage, ContentType: "image/png", SizeBytes: 1024}, "10.0.0.2:1000")
		assertMediaError(t, missing, http.StatusNotFound, "report_not_found")

		for name, input := range map[string]mediaUploadRequest{
			"mime": {MediaType: media.TypeImage, ContentType: "image/svg+xml", SizeBytes: 1024},
			"type": {MediaType: "document", ContentType: "application/pdf", SizeBytes: 1024},
			"size": {MediaType: media.TypeVideo, ContentType: "video/mp4", SizeBytes: 100<<20 + 1},
		} {
			t.Run(name, func(t *testing.T) {
				response := mediaRequest(t, handler, http.MethodPost, "/reports/"+reportID+"/media/uploads", input, "10.0.0.3:1000")
				assertMediaError(t, response, http.StatusBadRequest, "invalid_media")
			})
		}
	})

	t.Run("confirmation validation and storage failures", func(t *testing.T) {
		foreignKey := "reports/" + uuid.NewString() + "/media/foreign"
		response := mediaRequest(t, handler, http.MethodPost, "/reports/"+reportID+"/media", mediaConfirmRequest{ObjectKey: foreignKey, mediaUploadRequest: mediaUploadRequest{MediaType: media.TypeImage, ContentType: "image/png", SizeBytes: 1024}}, "10.0.0.4:1000")
		assertMediaError(t, response, http.StatusBadRequest, "invalid_object_key")

		newKey := media.ObjectKey(reportID)
		storage.headErr = errors.New("temporary storage failure")
		response = mediaRequest(t, handler, http.MethodPost, "/reports/"+reportID+"/media", mediaConfirmRequest{ObjectKey: newKey, mediaUploadRequest: mediaUploadRequest{MediaType: media.TypeImage, ContentType: "image/png", SizeBytes: 1024}}, "10.0.0.5:1000")
		assertMediaError(t, response, http.StatusServiceUnavailable, "media_storage_unavailable")
		storage.headErr = media.ErrObjectNotFound
		response = mediaRequest(t, handler, http.MethodPost, "/reports/"+reportID+"/media", mediaConfirmRequest{ObjectKey: newKey, mediaUploadRequest: mediaUploadRequest{MediaType: media.TypeImage, ContentType: "image/png", SizeBytes: 1024}}, "10.0.0.6:1000")
		assertMediaError(t, response, http.StatusBadRequest, "media_object_not_found")
		storage.headErr = nil
		storage.setObject(newKey, media.ObjectMetadata{ContentType: "image/jpeg", SizeBytes: 1024})
		response = mediaRequest(t, handler, http.MethodPost, "/reports/"+reportID+"/media", mediaConfirmRequest{ObjectKey: newKey, mediaUploadRequest: mediaUploadRequest{MediaType: media.TypeImage, ContentType: "image/png", SizeBytes: 1024}}, "10.0.0.7:1000")
		assertMediaError(t, response, http.StatusBadRequest, "media_metadata_mismatch")
	})

	key := media.ObjectKey(reportID)
	storage.setObject(key, media.ObjectMetadata{ContentType: "image/png", SizeBytes: 2048})
	validInput := mediaConfirmRequest{ObjectKey: key, mediaUploadRequest: mediaUploadRequest{MediaType: media.TypeImage, ContentType: "image/png", SizeBytes: 2048}}

	t.Run("successful attachment is idempotent and conflict-safe", func(t *testing.T) {
		response := mediaRequest(t, handler, http.MethodPost, "/reports/"+reportID+"/media", validInput, "10.0.0.8:1000")
		if response.Code != http.StatusCreated {
			t.Fatalf("attach status = %d, want 201", response.Code)
		}
		var first mediaRecord
		decodeMediaResponse(t, response, &first)
		callsAfterCreate := storage.calls()

		storage.headErr = errors.New("storage unavailable after attachment")
		response = mediaRequest(t, handler, http.MethodPost, "/reports/"+reportID+"/media", validInput, "10.0.0.9:1000")
		if response.Code != http.StatusOK {
			t.Fatalf("idempotent retry status = %d, want 200", response.Code)
		}
		var retry mediaRecord
		decodeMediaResponse(t, response, &retry)
		if retry.ID != first.ID || storage.calls() != callsAfterCreate {
			t.Fatalf("retry = %+v, head calls changed from %d to %d", retry, callsAfterCreate, storage.calls())
		}
		storage.headErr = nil

		conflict := validInput
		conflict.ContentType = "image/jpeg"
		response = mediaRequest(t, handler, http.MethodPost, "/reports/"+reportID+"/media", conflict, "10.0.0.10:1000")
		assertMediaError(t, response, http.StatusConflict, "media_conflict")

		otherReport, err := store.Ingest(ctx, "media-ownership-report", reports.IngestRequest{RawText: "Ownership conflict report"})
		if err != nil {
			t.Fatalf("create ownership report: %v", err)
		}
		ownershipKey := media.ObjectKey(reportID)
		if _, err := pool.Exec(ctx, `INSERT INTO report_media (report_id, object_key, media_type, content_type, size_bytes) VALUES ($1, $2, 'image', 'image/png', 2048)`, otherReport.ReportID, ownershipKey); err != nil {
			t.Fatalf("insert ownership conflict: %v", err)
		}
		response = mediaRequest(t, handler, http.MethodPost, "/reports/"+reportID+"/media", mediaConfirmRequest{ObjectKey: ownershipKey, mediaUploadRequest: mediaUploadRequest{MediaType: media.TypeImage, ContentType: "image/png", SizeBytes: 2048}}, "10.0.0.11:1000")
		assertMediaError(t, response, http.StatusConflict, "media_conflict")

		assertMediaCount(t, ctx, pool, key, 1)
		assertOutboxMediaCount(t, ctx, pool, reportID, 1)
		assertOutboxMediaForObjectCount(t, ctx, pool, key, 1)
	})

	t.Run("concurrent first confirmations create one record and event", func(t *testing.T) {
		concurrentKey := media.ObjectKey(reportID)
		storage.setObject(concurrentKey, media.ObjectMetadata{ContentType: "image/png", SizeBytes: 4096})
		input := mediaConfirmRequest{ObjectKey: concurrentKey, mediaUploadRequest: mediaUploadRequest{MediaType: media.TypeImage, ContentType: "image/png", SizeBytes: 4096}}
		responses := make(chan int, 2)
		var waitGroup sync.WaitGroup
		for index := 0; index < 2; index++ {
			waitGroup.Add(1)
			go func(index int) {
				defer waitGroup.Done()
				response := mediaRequest(t, handler, http.MethodPost, "/reports/"+reportID+"/media", input, fmt.Sprintf("10.0.1.%d:1000", index+1))
				responses <- response.Code
			}(index)
		}
		waitGroup.Wait()
		close(responses)
		for code := range responses {
			if code != http.StatusCreated && code != http.StatusOK {
				t.Fatalf("concurrent attach status = %d", code)
			}
		}
		assertMediaCount(t, ctx, pool, concurrentKey, 1)
		assertOutboxMediaCount(t, ctx, pool, reportID, 2)
		assertOutboxMediaForObjectCount(t, ctx, pool, concurrentKey, 1)
	})

	t.Run("attachment and outbox are atomic", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_media_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced media outbox failure'; END; $$; CREATE TRIGGER fail_media_outbox_trigger BEFORE INSERT ON outbox_events FOR EACH ROW WHEN (NEW.event_type = 'report.media_attached') EXECUTE FUNCTION fail_media_outbox()`); err != nil {
			t.Fatalf("create failure trigger: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS fail_media_outbox_trigger ON outbox_events; DROP FUNCTION IF EXISTS fail_media_outbox()`)
		}()
		atomicKey := media.ObjectKey(reportID)
		storage.setObject(atomicKey, media.ObjectMetadata{ContentType: "image/png", SizeBytes: 512})
		response := mediaRequest(t, handler, http.MethodPost, "/reports/"+reportID+"/media", mediaConfirmRequest{ObjectKey: atomicKey, mediaUploadRequest: mediaUploadRequest{MediaType: media.TypeImage, ContentType: "image/png", SizeBytes: 512}}, "10.0.2.1:1000")
		assertMediaError(t, response, http.StatusInternalServerError, "internal_error")
		assertMediaCount(t, ctx, pool, atomicKey, 0)
	})

	t.Run("media endpoints are rate limited", func(t *testing.T) {
		limitedHandler := NewHandlerWithMedia(nil, RateLimitConfig{PerClientRatePerMinute: 1, PerClientBurst: 1, GlobalRatePerMinute: 1, GlobalBurst: 1}, pool, storage)
		input := mediaUploadRequest{MediaType: media.TypeImage, ContentType: "image/png", SizeBytes: 1}
		if response := mediaRequest(t, limitedHandler, http.MethodPost, "/reports/"+reportID+"/media/uploads", input, "10.0.3.1:1000"); response.Code != http.StatusOK {
			t.Fatalf("first limited request status = %d", response.Code)
		}
		if response := mediaRequest(t, limitedHandler, http.MethodPost, "/reports/"+reportID+"/media/uploads", input, "10.0.3.1:1000"); response.Code != http.StatusTooManyRequests {
			t.Fatalf("second limited request status = %d, want 429", response.Code)
		}
	})
}

func mediaRequest(t *testing.T, handler http.Handler, method, path string, body any, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal media request: %v", err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.RemoteAddr = remoteAddr
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeMediaResponse(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatalf("decode media response: %v; body=%s", err, response.Body.String())
	}
}

func assertMediaError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, status, response.Body.String())
	}
	var body errorResponse
	decodeMediaResponse(t, response, &body)
	if body.Error.Code != code {
		t.Fatalf("error code = %q, want %q", body.Error.Code, code)
	}
}

func assertMediaCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, objectKey string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM report_media WHERE object_key = $1`, objectKey).Scan(&count); err != nil {
		t.Fatalf("media count: %v", err)
	}
	if count != want {
		t.Fatalf("media count = %d, want %d", count, want)
	}
}

func assertOutboxMediaCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, reportID string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type = 'report.media_attached' AND aggregate_id = $1`, reportID).Scan(&count); err != nil {
		t.Fatalf("media outbox count: %v", err)
	}
	if count != want {
		t.Fatalf("media outbox count = %d, want %d", count, want)
	}
}

func assertOutboxMediaForObjectCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, objectKey string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM outbox_events AS event
		JOIN report_media AS media ON event.payload->>'media_id' = media.id::text
		WHERE event.event_type = 'report.media_attached' AND media.object_key = $1
	`, objectKey).Scan(&count); err != nil {
		t.Fatalf("media object outbox count: %v", err)
	}
	if count != want {
		t.Fatalf("media object outbox count = %d, want %d", count, want)
	}
}

func createMediaIntegrationDatabase(t *testing.T, ctx context.Context, databaseURL string) (string, *pgx.Conn, string) {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	databaseName := fmt.Sprintf("signa_media_test_%d", time.Now().UnixNano())
	maintenanceURL := *parsed
	maintenanceURL.Path = "/postgres"
	maintenance, err := pgx.Connect(ctx, maintenanceURL.String())
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	if _, err := maintenance.Exec(ctx, `CREATE DATABASE `+databaseName); err != nil {
		_ = maintenance.Close(context.Background())
		t.Fatalf("create test database: %v", err)
	}
	testURL := *parsed
	testURL.Path = "/" + databaseName
	return testURL.String(), maintenance, databaseName
}

func runMediaGoose(t *testing.T, ctx context.Context, databaseURL, command string) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	goose := exec.CommandContext(ctx, "go", "run", "github.com/pressly/goose/v3/cmd/goose@v3.27.0", "-dir", "migrations", "postgres", databaseURL, command)
	goose.Dir = repoRoot
	if output, err := goose.CombinedOutput(); err != nil {
		t.Fatalf("goose %s: %v\n%s", command, err, output)
	}
}
