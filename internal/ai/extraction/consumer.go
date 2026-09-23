package extraction

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	goRedis "github.com/redis/go-redis/v9"
)

// StreamClient is the small Redis Streams surface required by the extractor.
// Keeping it narrow makes processing tests independent of Redis and preserves
// the retry behavior at the consumer boundary.
type StreamClient interface {
	EnsureGroup(context.Context, string, string) error
	Read(context.Context, string, string, string, bool, time.Duration) ([]StreamMessage, error)
	Ack(context.Context, string, string, string) error
}

type MessageProcessor interface {
	Process(context.Context, StreamMessage) error
}

type Consumer struct {
	client     StreamClient
	processor  MessageProcessor
	stream     string
	group      string
	consumer   string
	pollPeriod time.Duration
	logger     *slog.Logger
}

func NewConsumer(client StreamClient, processor MessageProcessor, stream, group, consumer string, pollPeriod time.Duration) *Consumer {
	return &Consumer{
		client:     client,
		processor:  processor,
		stream:     stream,
		group:      group,
		consumer:   consumer,
		pollPeriod: pollPeriod,
	}
}

func (c *Consumer) WithLogger(logger *slog.Logger) *Consumer {
	if c != nil {
		c.logger = logger
	}
	return c
}

func (c *Consumer) Run(ctx context.Context) error {
	if c == nil || c.client == nil || c.processor == nil {
		return fmt.Errorf("extraction consumer dependencies are required")
	}
	if c.stream == "" || c.group == "" || c.consumer == "" {
		return fmt.Errorf("extraction consumer stream, group, and consumer are required")
	}
	if c.pollPeriod <= 0 {
		return fmt.Errorf("extraction consumer poll period must be greater than zero")
	}
	if err := c.client.EnsureGroup(ctx, c.stream, c.group); err != nil {
		return fmt.Errorf("ensure extraction consumer group: %w", err)
	}

	for {
		if ctx.Err() != nil {
			return nil
		}

		messages, err := c.client.Read(ctx, c.stream, c.group, c.consumer, true, 0)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read pending extraction messages: %w", err)
		}
		if len(messages) == 0 {
			messages, err = c.client.Read(ctx, c.stream, c.group, c.consumer, false, c.pollPeriod)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("read new extraction messages: %w", err)
			}
		}

		for _, message := range messages {
			if err := c.processor.Process(ctx, message); err != nil {
				if c.logger != nil {
					c.logger.Error("report extraction failed; message remains pending", "stream_message_id", message.ID, "error", err)
				}
				if err := wait(ctx, c.pollPeriod); err != nil {
					return nil
				}
				continue
			}
		}
	}
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type RedisStreamClient struct {
	client *goRedis.Client
}

func NewRedisStreamClient(client *goRedis.Client) *RedisStreamClient {
	return &RedisStreamClient{client: client}
}

func (c *RedisStreamClient) EnsureGroup(ctx context.Context, stream, group string) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("redis stream client is required")
	}
	// Start at the beginning so events published before the first worker
	// startup are not skipped. Existing groups retain their own cursor.
	err := c.client.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

func (c *RedisStreamClient) Read(ctx context.Context, stream, group, consumer string, pending bool, block time.Duration) ([]StreamMessage, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("redis stream client is required")
	}
	id := ">"
	if pending {
		id = "0"
		block = 0
	}
	entries, err := c.client.XReadGroup(ctx, &goRedis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{stream, id},
		Count:    1,
		Block:    block,
	}).Result()
	if err != nil {
		if err == goRedis.Nil {
			return nil, nil
		}
		return nil, err
	}

	messages := make([]StreamMessage, 0, len(entries))
	for _, entry := range entries {
		for _, message := range entry.Messages {
			messages = append(messages, StreamMessage{ID: message.ID, Values: message.Values})
		}
	}
	return messages, nil
}

func (c *RedisStreamClient) Ack(ctx context.Context, stream, group, messageID string) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("redis stream client is required")
	}
	return c.client.XAck(ctx, stream, group, messageID).Err()
}
