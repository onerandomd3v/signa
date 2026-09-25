//go:build integration

package extraction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	goRedis "github.com/redis/go-redis/v9"
)

func TestConsumerRetriesPendingMessageAndAcknowledgesAfterSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	redisAddr := os.Getenv("SIGNA_REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	redisClient := goRedis.NewClient(&goRedis.Options{Addr: redisAddr})
	defer redisClient.Close()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping Redis: %v", err)
	}

	stream := "signa:test:ai-extraction:" + time.Now().Format("20060102150405.000000000")
	group := "signa:test:ai-extraction-group:" + time.Now().Format("20060102150405.000000000")
	defer redisClient.XGroupDestroy(context.Background(), stream, group)
	defer redisClient.Del(context.Background(), stream)

	if _, err := redisClient.XAdd(ctx, &goRedis.XAddArgs{Stream: stream, Values: reportCreatedFields()}).Result(); err != nil {
		t.Fatalf("add stream message: %v", err)
	}

	_, file, _, _ := runtime.Caller(0)
	schemaPath := filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts", "ai", "extraction", "v0", "schema.json")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := NewValidator(schema)
	if err != nil {
		t.Fatal(err)
	}
	provider := &sequencedProvider{outputs: [][]byte{nil, []byte(validExtractionJSON())}, errors: []error{transientProviderError("test provider", errors.New("provider unavailable")), nil}}
	observer := &recordingObserver{}
	streamClient := NewRedisStreamClient(redisClient)
	processor := NewProcessor(
		staticReportReader{},
		provider,
		validator,
		observer,
		streamClient,
		stream,
		group,
	)
	consumer := NewConsumer(streamClient, processor, stream, group, "consumer-1", 20*time.Millisecond)

	runErr := make(chan error, 1)
	go func() { runErr <- consumer.Run(ctx) }()

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		pending, pendingErr := redisClient.XPending(ctx, stream, group).Result()
		if pendingErr == nil && provider.Calls() >= 2 && pending.Count == 0 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("message was not retried and acknowledged; calls=%d pending=%+v error=%v", provider.Calls(), pending, pendingErr)
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("consumer Run() error = %v", err)
	}
	if len(observer.results) != 1 {
		t.Fatalf("observed results = %d, want 1", len(observer.results))
	}
}

func TestConsumerConsumesMessageAddedBeforeGroupCreation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	redisAddr := os.Getenv("SIGNA_REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	redisClient := goRedis.NewClient(&goRedis.Options{Addr: redisAddr})
	defer redisClient.Close()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping Redis: %v", err)
	}

	stream := "signa:test:ai-extraction-before-group:" + time.Now().Format("20060102150405.000000000")
	group := "signa:test:ai-extraction-before-group:" + time.Now().Format("20060102150405.000000000")
	defer redisClient.XGroupDestroy(context.Background(), stream, group)
	defer redisClient.Del(context.Background(), stream)
	if _, err := redisClient.XAdd(ctx, &goRedis.XAddArgs{Stream: stream, Values: reportCreatedFields()}).Result(); err != nil {
		t.Fatalf("add stream message before group creation: %v", err)
	}

	_, file, _, _ := runtime.Caller(0)
	schemaPath := filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts", "ai", "extraction", "v0", "schema.json")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := NewValidator(schema)
	if err != nil {
		t.Fatal(err)
	}
	provider := &sequencedProvider{outputs: [][]byte{[]byte(validExtractionJSON())}, errors: []error{nil}}
	observer := &recordingObserver{}
	streamClient := NewRedisStreamClient(redisClient)
	processor := NewProcessor(staticReportReader{}, provider, validator, observer, streamClient, stream, group)
	consumer := NewConsumer(streamClient, processor, stream, group, "consumer-before-group", 20*time.Millisecond)

	runErr := make(chan error, 1)
	go func() { runErr <- consumer.Run(ctx) }()

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for provider.Calls() < 1 {
		select {
		case <-deadline.C:
			t.Fatal("message added before group creation was not consumed")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("consumer Run() error = %v", err)
	}
	if len(observer.results) != 1 {
		t.Fatalf("observed results = %d, want 1", len(observer.results))
	}
}

type staticReportReader struct{}

func (staticReportReader) LoadRawText(context.Context, string) (string, error) {
	return "Road blocked near the market", nil
}

type sequencedProvider struct {
	mu      sync.Mutex
	outputs [][]byte
	errors  []error
	calls   int
}

func (p *sequencedProvider) Extract(context.Context, string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := p.calls
	p.calls++
	if p.errors[index] != nil {
		return nil, p.errors[index]
	}
	return p.outputs[index], nil
}

func (p *sequencedProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type recordingObserver struct {
	results []Extraction
}

func (o *recordingObserver) Observe(_ context.Context, _ string, result Extraction) error {
	o.results = append(o.results, result)
	return nil
}
