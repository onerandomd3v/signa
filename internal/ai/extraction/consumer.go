package extraction

import (
	"context"
	"errors"
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

// PendingClaimer lets a replacement consumer reclaim messages left pending by
// a stopped consumer. Test clients may omit it and use the basic pending read.
type PendingClaimer interface {
	ClaimPending(context.Context, string, string, string) ([]StreamMessage, error)
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

	blockedMessageIDs := make(map[string]struct{})
	for {
		if ctx.Err() != nil {
			return nil
		}

		messages, err := readPending(ctx, c.client, c.stream, c.group, c.consumer)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read pending extraction messages: %w", err)
		}
		messages = filterBlockedMessages(messages, blockedMessageIDs)
		processMessages := func(batch []StreamMessage) error {
			for _, message := range batch {
				if _, blocked := blockedMessageIDs[message.ID]; blocked {
					continue
				}
				if err := c.processor.Process(ctx, message); err != nil {
					if errors.Is(err, ErrDurableExtractionDestinationUnresolved) {
						blockedMessageIDs[message.ID] = struct{}{}
						continue
					}
					if c.logger != nil {
						c.logger.Error("report extraction failed; message remains pending", "stream_message_id", message.ID, "error", err)
					}
					if err := wait(ctx, c.pollPeriod); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if err := processMessages(messages); err != nil {
			return nil
		}
		newMessages, err := c.client.Read(ctx, c.stream, c.group, c.consumer, false, c.pollPeriod)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read new extraction messages: %w", err)
		}
		newMessages = filterBlockedMessages(newMessages, blockedMessageIDs)
		if err := processMessages(newMessages); err != nil {
			return nil
		}
	}
}

func readPending(ctx context.Context, client StreamClient, stream, group, consumer string) ([]StreamMessage, error) {
	messages, err := client.Read(ctx, stream, group, consumer, true, 0)
	if err != nil {
		return messages, err
	}
	if claimer, ok := client.(PendingClaimer); ok {
		claimed, claimErr := claimer.ClaimPending(ctx, stream, group, consumer)
		if claimErr != nil {
			return nil, claimErr
		}
		return MergeMessages(messages, claimed), nil
	}
	return messages, nil
}

func MergeMessages(groups ...[]StreamMessage) []StreamMessage {
	seen := make(map[string]struct{})
	merged := make([]StreamMessage, 0)
	for _, messages := range groups {
		for _, message := range messages {
			if _, ok := seen[message.ID]; ok {
				continue
			}
			seen[message.ID] = struct{}{}
			merged = append(merged, message)
		}
	}
	return merged
}

func filterBlockedMessages(messages []StreamMessage, blocked map[string]struct{}) []StreamMessage {
	if len(blocked) == 0 {
		return messages
	}
	filtered := make([]StreamMessage, 0, len(messages))
	for _, message := range messages {
		if _, ok := blocked[message.ID]; !ok {
			filtered = append(filtered, message)
		}
	}
	return filtered
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
		Count:    100,
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

func (c *RedisStreamClient) ClaimPending(ctx context.Context, stream, group, consumer string) ([]StreamMessage, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("redis stream client is required")
	}
	messages, _, err := c.client.XAutoClaim(ctx, &goRedis.XAutoClaimArgs{
		Stream: stream, Group: group, Consumer: consumer, MinIdle: 30 * time.Second, Start: "0", Count: 100,
	}).Result()
	if err != nil {
		return nil, err
	}
	result := make([]StreamMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, StreamMessage{ID: message.ID, Values: message.Values})
	}
	return result, nil
}
