package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/ai/extraction"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/logging"
	"github.com/onerandomd3v/signa/internal/outbox"
	"github.com/onerandomd3v/signa/internal/worker"
	goRedis "github.com/redis/go-redis/v9"
)

func main() {
	logger := logging.New()
	if err := run(context.Background(), logger); err != nil {
		logger.Error("worker stopped with error", "error", err)
		os.Exit(1)
	}
}

func run(parent context.Context, logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Ping(ctx); err != nil {
		return err
	}

	redisClient := goRedis.NewClient(&goRedis.Options{Addr: cfg.RedisAddr})
	defer func() { _ = redisClient.Close() }()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return err
	}

	publisher := outbox.NewPublisher(database, redisClient, logger)
	schema, err := os.ReadFile(cfg.AISchemaPath)
	if err != nil {
		return fmt.Errorf("read AI extraction schema: %w", err)
	}
	validator, err := extraction.NewValidator(schema)
	if err != nil {
		return err
	}
	provider, err := extraction.NewOpenAIProvider(extraction.OpenAIConfig{
		APIKey:  cfg.OpenAIAPIKey,
		Model:   cfg.OpenAIModel,
		BaseURL: cfg.OpenAIBaseURL,
		Schema:  schema,
	})
	if err != nil {
		return err
	}

	streamClient := extraction.NewRedisStreamClient(redisClient)
	observer := extraction.LoggingObserver{Logger: logger}
	processor := extraction.NewProcessor(
		extraction.NewPostgresReportReader(database),
		provider,
		validator,
		observer,
		streamClient,
		outbox.ReportEventsStream,
		cfg.AIConsumerGroup,
	)
	consumerName := cfg.AIConsumerName
	if consumerName == "" {
		hostname, hostnameErr := os.Hostname()
		if hostnameErr != nil || hostname == "" {
			hostname = "worker"
		}
		consumerName = hostname
	}
	consumer := extraction.NewConsumer(
		streamClient,
		processor,
		outbox.ReportEventsStream,
		cfg.AIConsumerGroup,
		consumerName,
		cfg.AIPollInterval,
	).WithLogger(logger)

	errCh := make(chan error, 2)
	go func() { errCh <- worker.Run(ctx, logger, cfg.WorkerInterval, publisher) }()
	go func() { errCh <- consumer.Run(ctx) }()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}
