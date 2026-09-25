package delivery

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
)

type StreamClient interface {
	EnsureGroup(context.Context, string, string) error
	Read(context.Context, string, string, string, bool, time.Duration) ([]extraction.StreamMessage, error)
	Ack(context.Context, string, string, string) error
}

type PendingClaimer interface {
	ClaimPending(context.Context, string, string, string) ([]extraction.StreamMessage, error)
}

type Consumer struct {
	client                  StreamClient
	processor               *Processor
	stream, group, consumer string
	poll                    time.Duration
	logger                  *slog.Logger
}

func NewConsumer(client StreamClient, processor *Processor, stream, group, consumer string, poll time.Duration) *Consumer {
	return &Consumer{client: client, processor: processor, stream: stream, group: group, consumer: consumer, poll: poll}
}

func (c *Consumer) WithLogger(logger *slog.Logger) *Consumer { c.logger = logger; return c }

func (c *Consumer) Run(ctx context.Context) error {
	if c == nil || c.client == nil || c.processor == nil || c.stream == "" || c.group == "" || c.consumer == "" || c.poll <= 0 {
		return fmt.Errorf("delivery consumer configuration is invalid")
	}
	if err := c.client.EnsureGroup(ctx, c.stream, c.group); err != nil {
		return fmt.Errorf("ensure delivery consumer group: %w", err)
	}
	for ctx.Err() == nil {
		pending, err := c.readPending(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := c.process(ctx, pending); err != nil {
			return err
		}
		messages, err := c.client.Read(ctx, c.stream, c.group, c.consumer, false, c.poll)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read delivery messages: %w", err)
		}
		if err := c.process(ctx, messages); err != nil {
			return err
		}
	}
	return nil
}

func (c *Consumer) readPending(ctx context.Context) ([]extraction.StreamMessage, error) {
	messages, err := c.client.Read(ctx, c.stream, c.group, c.consumer, true, 0)
	if err != nil {
		return nil, err
	}
	if claimer, ok := c.client.(PendingClaimer); ok {
		claimed, err := claimer.ClaimPending(ctx, c.stream, c.group, c.consumer)
		if err != nil {
			return nil, err
		}
		return extraction.MergeMessages(messages, claimed), nil
	}
	return messages, nil
}

func (c *Consumer) process(ctx context.Context, messages []extraction.StreamMessage) error {
	for _, message := range extraction.MergeMessages(messages) {
		result, err := c.processor.Process(ctx, message)
		if err != nil {
			if c.logger != nil {
				c.logger.Error("delivery processing failed; message remains pending", "stream_message_id", message.ID, "error", err)
			}
			continue
		}
		if !result.Ack {
			if err := wait(ctx, result.RetryAfterOr(c.poll)); err != nil {
				return err
			}
			continue
		}
		if err := c.client.Ack(ctx, c.stream, c.group, message.ID); err != nil {
			return fmt.Errorf("ack delivery message %s: %w", message.ID, err)
		}
	}
	return nil
}

func (r ProcessResult) RetryAfterOr(fallback time.Duration) time.Duration {
	if r.RetryAfter > 0 {
		return r.RetryAfter
	}
	return fallback
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
