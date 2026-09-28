package verification

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
	"github.com/onerandomd3v/signa/internal/incidents"
)

// TargetingConsumer owns verification-targeting acknowledgements separately
// from lifecycle processing. Every successfully handled or ignored incident
// event is acknowledged in this consumer's own group; errors remain pending.
type TargetingConsumer struct {
	client    incidents.StreamClient
	processor extraction.MessageProcessor
	stream    string
	group     string
	consumer  string
	poll      time.Duration
	logger    *slog.Logger
}

func NewTargetingConsumer(client incidents.StreamClient, processor extraction.MessageProcessor, stream, group, consumer string, poll time.Duration) *TargetingConsumer {
	return &TargetingConsumer{client: client, processor: processor, stream: stream, group: group, consumer: consumer, poll: poll}
}

func (c *TargetingConsumer) WithLogger(logger *slog.Logger) *TargetingConsumer {
	if c != nil {
		c.logger = logger
	}
	return c
}

func (c *TargetingConsumer) Run(ctx context.Context) error {
	if c == nil || c.client == nil || c.processor == nil || c.stream == "" || c.group == "" || c.consumer == "" || c.poll <= 0 {
		return fmt.Errorf("verification targeting consumer configuration is invalid")
	}
	if err := c.client.EnsureGroup(ctx, c.stream, c.group); err != nil {
		return err
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
			return err
		}
		if err := c.process(ctx, messages); err != nil {
			return err
		}
	}
	return nil
}

func (c *TargetingConsumer) readPending(ctx context.Context) ([]incidents.StreamMessage, error) {
	messages, err := c.client.Read(ctx, c.stream, c.group, c.consumer, true, 0)
	if err != nil {
		return messages, err
	}
	if claimer, ok := c.client.(extraction.PendingClaimer); ok {
		claimed, claimErr := claimer.ClaimPending(ctx, c.stream, c.group, c.consumer)
		if claimErr != nil {
			return nil, claimErr
		}
		return extraction.MergeMessages(messages, claimed), nil
	}
	return messages, nil
}

func (c *TargetingConsumer) process(ctx context.Context, messages []incidents.StreamMessage) error {
	for _, message := range extraction.MergeMessages(messages) {
		if err := c.processor.Process(ctx, message); err != nil {
			if c.logger != nil {
				c.logger.Error("verification targeting failed; message remains pending", "stream_message_id", message.ID, "error", err)
			}
			continue
		}
		if err := c.client.Ack(ctx, c.stream, c.group, message.ID); err != nil {
			return err
		}
	}
	return nil
}
