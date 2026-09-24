package incidents

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
)

type LifecycleConsumer struct {
	client    StreamClient
	processor interface {
		Process(context.Context, StreamMessage) error
	}
	stream, group, consumer string
	poll                    time.Duration
	logger                  *slog.Logger
}

func NewLifecycleConsumer(client StreamClient, processor interface {
	Process(context.Context, StreamMessage) error
}, stream, group, consumer string, poll time.Duration) *LifecycleConsumer {
	return &LifecycleConsumer{client: client, processor: processor, stream: stream, group: group, consumer: consumer, poll: poll}
}

func (c *LifecycleConsumer) WithLogger(logger *slog.Logger) *LifecycleConsumer {
	if c != nil {
		c.logger = logger
	}
	return c
}

func (c *LifecycleConsumer) Run(ctx context.Context) error {
	if c == nil || c.client == nil || c.processor == nil || c.stream == "" || c.group == "" || c.consumer == "" || c.poll <= 0 {
		return fmt.Errorf("lifecycle consumer configuration is invalid")
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

func (c *LifecycleConsumer) process(ctx context.Context, messages []StreamMessage) error {
	for _, message := range extraction.MergeMessages(messages) {
		name, ok := streamString(message.Values, "event_name")
		if !ok {
			continue
		}
		knownInput := name == IncidentCreatedV1 || name == IncidentReportAttachedV1
		knownOutput := name == IncidentStatusChangedV1 || name == IncidentResolvedV1 || name == IncidentConfidenceChangedV1 || name == IncidentSeverityChangedV1
		if knownInput {
			if err := c.processor.Process(ctx, message); err != nil {
				if c.logger != nil {
					c.logger.Error("incident lifecycle evaluation failed; message remains pending", "stream_message_id", message.ID, "event_name", name, "error", err)
				}
				continue
			}
		} else if !knownOutput {
			// Unknown events remain pending for inspection.
			continue
		}
		if err := c.client.Ack(ctx, c.stream, c.group, message.ID); err != nil {
			return err
		}
	}
	return nil
}

func (p *LifecycleProcessor) RunSweep(ctx context.Context, interval time.Duration, logger *slog.Logger) error {
	if interval <= 0 {
		return fmt.Errorf("lifecycle sweep interval must be greater than zero")
	}
	if err := p.Sweep(ctx, 100); err != nil && ctx.Err() == nil && logger != nil {
		logger.Error("incident lifecycle sweep failed", "error", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := p.Sweep(ctx, 100); err != nil && ctx.Err() == nil && logger != nil {
				logger.Error("incident lifecycle sweep failed", "error", err)
			}
		}
	}
}
