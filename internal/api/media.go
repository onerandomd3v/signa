package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/media"
)

type mediaHandler struct {
	logger  *slog.Logger
	pool    *pgxpool.Pool
	storage media.Storage
}

type mediaUploadRequest struct {
	MediaType   string `json:"media_type"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

type mediaConfirmRequest struct {
	ObjectKey string `json:"object_key"`
	mediaUploadRequest
}

type uploadAuthorization struct {
	ObjectKey       string            `json:"object_key"`
	UploadURL       string            `json:"upload_url"`
	RequiredHeaders map[string]string `json:"required_headers"`
	ExpiresAt       string            `json:"expires_at"`
}

type mediaRecord struct {
	ID          string    `json:"id"`
	ReportID    string    `json:"report_id"`
	ObjectKey   string    `json:"object_key"`
	MediaType   string    `json:"media_type"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
}

func (h *mediaHandler) authorizeUpload(writer http.ResponseWriter, request *http.Request) {
	reportID, err := uuid.Parse(chi.URLParam(request, "report_id"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "report_not_found", "report was not found")
		return
	}
	var input mediaUploadRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body must be valid JSON")
		return
	}
	input.MediaType = strings.TrimSpace(input.MediaType)
	input.ContentType = strings.ToLower(strings.TrimSpace(input.ContentType))
	if err := media.Validate(input.MediaType, input.ContentType, input.SizeBytes); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_media", err.Error())
		return
	}
	if h.pool == nil || !h.reportExists(request.Context(), reportID) {
		writeError(writer, http.StatusNotFound, "report_not_found", "report was not found")
		return
	}
	if h.storage == nil {
		writeError(writer, http.StatusServiceUnavailable, "media_storage_unavailable", "media storage is not configured")
		return
	}
	objectKey := media.ObjectKey(reportID.String())
	uploadURL, headers, expiresAt, err := h.storage.PresignPut(request.Context(), objectKey, input.ContentType, media.UploadURLLifetime)
	if err != nil {
		h.logError("authorize media upload", err)
		writeError(writer, http.StatusServiceUnavailable, "media_storage_unavailable", "media storage is unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, uploadAuthorization{ObjectKey: objectKey, UploadURL: uploadURL, RequiredHeaders: headers, ExpiresAt: expiresAt.UTC().Format(time.RFC3339Nano)})
}

func (h *mediaHandler) confirmUpload(writer http.ResponseWriter, request *http.Request) {
	reportID, err := uuid.Parse(chi.URLParam(request, "report_id"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "report_not_found", "report was not found")
		return
	}
	var input mediaConfirmRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body must be valid JSON")
		return
	}
	input.MediaType = strings.TrimSpace(input.MediaType)
	input.ContentType = strings.ToLower(strings.TrimSpace(input.ContentType))
	if !media.IsObjectKeyForReport(input.ObjectKey, reportID.String()) {
		writeError(writer, http.StatusBadRequest, "invalid_object_key", "object_key is not owned by this report")
		return
	}
	if err := media.Validate(input.MediaType, input.ContentType, input.SizeBytes); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_media", err.Error())
		return
	}
	if h.pool == nil || !h.reportExists(request.Context(), reportID) {
		writeError(writer, http.StatusNotFound, "report_not_found", "report was not found")
		return
	}
	existing, found, err := h.findMedia(request.Context(), input.ObjectKey)
	if err != nil {
		h.logError("load existing media attachment", err)
		writeError(writer, http.StatusInternalServerError, "internal_error", "unable to load media attachment")
		return
	}
	if found {
		if !sameMediaAttachment(existing, reportID, input) {
			writeError(writer, http.StatusConflict, "media_conflict", "object_key is already attached with different metadata")
			return
		}
		writeJSON(writer, http.StatusOK, existing)
		return
	}
	if h.storage == nil {
		writeError(writer, http.StatusServiceUnavailable, "media_storage_unavailable", "media storage is not configured")
		return
	}
	object, err := h.storage.Head(request.Context(), input.ObjectKey)
	if err != nil {
		if errors.Is(err, media.ErrObjectNotFound) {
			writeError(writer, http.StatusBadRequest, "media_object_not_found", "uploaded media object was not found")
			return
		}
		h.logError("inspect media upload", err)
		writeError(writer, http.StatusServiceUnavailable, "media_storage_unavailable", "media storage is unavailable")
		return
	}
	if object.SizeBytes != input.SizeBytes || strings.ToLower(object.ContentType) != input.ContentType {
		writeError(writer, http.StatusBadRequest, "media_metadata_mismatch", "uploaded object metadata does not match the request")
		return
	}
	record, created, err := h.persistMedia(request.Context(), reportID, input)
	if err != nil {
		if errors.Is(err, errMediaConflict) {
			writeError(writer, http.StatusConflict, "media_conflict", "object_key is already attached with different metadata")
			return
		}
		h.logError("persist media attachment", err)
		writeError(writer, http.StatusInternalServerError, "internal_error", "unable to attach media")
		return
	}
	if created {
		writeJSON(writer, http.StatusCreated, record)
		return
	}
	writeJSON(writer, http.StatusOK, record)
}

var errMediaConflict = errors.New("media object conflict")

func (h *mediaHandler) reportExists(ctx context.Context, id uuid.UUID) bool {
	var exists bool
	if err := h.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM reports WHERE id = $1)`, id).Scan(&exists); err != nil {
		return false
	}
	return exists
}

func (h *mediaHandler) findMedia(ctx context.Context, objectKey string) (mediaRecord, bool, error) {
	var record mediaRecord
	var created time.Time
	err := h.pool.QueryRow(ctx, `SELECT id::text, report_id::text, object_key, media_type, content_type, size_bytes, created_at FROM report_media WHERE object_key = $1`, objectKey).Scan(&record.ID, &record.ReportID, &record.ObjectKey, &record.MediaType, &record.ContentType, &record.SizeBytes, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return mediaRecord{}, false, nil
	}
	if err != nil {
		return mediaRecord{}, false, err
	}
	record.CreatedAt = created.UTC()
	return record, true, nil
}

func sameMediaAttachment(record mediaRecord, reportID uuid.UUID, input mediaConfirmRequest) bool {
	return record.ReportID == reportID.String() && record.MediaType == input.MediaType && record.ContentType == input.ContentType && record.SizeBytes == input.SizeBytes
}

func (h *mediaHandler) persistMedia(ctx context.Context, reportID uuid.UUID, input mediaConfirmRequest) (mediaRecord, bool, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return mediaRecord{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var record mediaRecord
	var created time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO report_media (report_id, object_key, media_type, content_type, size_bytes)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (object_key) DO NOTHING
		RETURNING id::text, report_id::text, object_key, media_type, content_type, size_bytes, created_at
	`, reportID, input.ObjectKey, input.MediaType, input.ContentType, input.SizeBytes).Scan(&record.ID, &record.ReportID, &record.ObjectKey, &record.MediaType, &record.ContentType, &record.SizeBytes, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id::text, report_id::text, object_key, media_type, content_type, size_bytes, created_at FROM report_media WHERE object_key = $1`, input.ObjectKey).Scan(&record.ID, &record.ReportID, &record.ObjectKey, &record.MediaType, &record.ContentType, &record.SizeBytes, &created)
		if err != nil {
			return mediaRecord{}, false, err
		}
		if !sameMediaAttachment(record, reportID, input) {
			return mediaRecord{}, false, errMediaConflict
		}
		record.CreatedAt = created.UTC()
		if err := tx.Commit(ctx); err != nil {
			return mediaRecord{}, false, err
		}
		return record, false, nil
	}
	if err != nil {
		return mediaRecord{}, false, err
	}
	payload := fmt.Sprintf(`{"report_id":%q,"media_id":%q}`, reportID.String(), record.ID)
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload) VALUES ('report.media_attached', 'report', $1, $2::jsonb)`, reportID, payload); err != nil {
		return mediaRecord{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return mediaRecord{}, false, err
	}
	record.CreatedAt = created.UTC()
	return record, true, nil
}

func (h *mediaHandler) logError(message string, err error) {
	if h.logger != nil {
		h.logger.Error(message, "error", err)
	}
}

func decodeJSON(request *http.Request, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}
