package incidents

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
)

type EvidencePolicyConsumer struct {
	client    StreamClient
	processor interface {
		Process(context.Context, StreamMessage) error
	}
	stream, group, consumer string
	poll                    time.Duration
	logger                  *slog.Logger
}

func (c *EvidencePolicyConsumer) WithLogger(logger *slog.Logger) *EvidencePolicyConsumer {
	if c != nil {
		c.logger = logger
	}
	return c
}

func NewEvidencePolicyConsumer(client StreamClient, processor interface {
	Process(context.Context, StreamMessage) error
}, stream, group, consumer string, poll time.Duration) *EvidencePolicyConsumer {
	return &EvidencePolicyConsumer{client: client, processor: processor, stream: stream, group: group, consumer: consumer, poll: poll}
}

func (c *EvidencePolicyConsumer) Run(ctx context.Context) error {
	if c == nil || c.client == nil || c.processor == nil || c.stream == "" || c.group == "" || c.consumer == "" || c.poll <= 0 {
		return fmt.Errorf("evidence policy consumer configuration is invalid")
	}
	if err := c.client.EnsureGroup(ctx, c.stream, c.group); err != nil {
		return err
	}
	for ctx.Err() == nil {
		pending, err := readPending(ctx, c.client, c.stream, c.group, c.consumer)
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

func (c *EvidencePolicyConsumer) process(ctx context.Context, messages []StreamMessage) error {
	for _, message := range extraction.MergeMessages(messages) {
		name, ok := streamString(message.Values, "event_name")
		if !ok {
			continue
		}
		switch name {
		case IncidentCreatedV1, IncidentReportAttachedV1:
			if err := c.processor.Process(ctx, message); err != nil {
				if c.logger != nil {
					name, _ := streamString(message.Values, "event_name")
					c.logger.Error("incident confidence evaluation failed; message remains pending", "stream_message_id", message.ID, "event_name", name, "error", err)
				}
				continue
			}
		case IncidentConfidenceChangedV1, IncidentSeverityChangedV1:
			// These are outputs of this processor and must not feed it again.
		default:
			// Unknown or unrelated events remain pending for inspection.
			continue
		}
		if err := c.client.Ack(ctx, c.stream, c.group, message.ID); err != nil {
			return err
		}
	}
	return nil
}
