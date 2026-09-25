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
	"github.com/onerandomd3v/signa/internal/ai/similarity"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/incidents"
	"github.com/onerandomd3v/signa/internal/incidents/confidence"
	"github.com/onerandomd3v/signa/internal/incidents/lifecycle"
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
	incidentPolicy, err := config.LoadIncidentPolicy()
	if err != nil {
		return err
	}
	confidencePolicyConfig, err := config.LoadConfidencePolicy()
	if err != nil {
		return err
	}
	coordinationSyncWindow, err := config.LoadCoordinationSyncWindow()
	if err != nil {
		return err
	}
	lifecyclePolicyConfig, err := config.LoadIncidentLifecyclePolicy()
	if err != nil {
		return err
	}
	if _, err := config.LoadPriorityPolicy(); err != nil {
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
	generationSchema, err := os.ReadFile(cfg.AIGenerationSchemaPath)
	if err != nil {
		return fmt.Errorf("read OpenAI generation schema: %w", err)
	}
	provider, err := extraction.NewOpenAIProvider(extraction.OpenAIConfig{
		APIKey:           cfg.OpenAIAPIKey,
		Model:            cfg.OpenAIModel,
		BaseURL:          cfg.OpenAIBaseURL,
		GenerationSchema: generationSchema,
	})
	if err != nil {
		return err
	}
	retryingProvider, err := extraction.NewRetryingProvider(provider, extraction.RetryConfig{
		Timeout:     cfg.AIProviderTimeout,
		MaxAttempts: cfg.AIRetryMaxAttempts,
		Backoff:     cfg.AIRetryBackoff,
	})
	if err != nil {
		return err
	}

	streamClient := extraction.NewRedisStreamClient(redisClient)
	observer := extraction.NewDurableStore(database, validator)
	processor := extraction.NewProcessor(
		extraction.NewPostgresReportReader(database),
		retryingProvider,
		validator,
		observer,
		streamClient,
		outbox.ReportEventsStream,
		cfg.AIConsumerGroup,
	).WithMetrics(extraction.NewSlogMetrics(logger))
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

	similaritySchema, err := os.ReadFile("contracts/ai/similarity/v1/schema.json")
	if err != nil {
		return fmt.Errorf("read similarity schema: %w", err)
	}
	similarityValidator, err := similarity.NewValidator(similaritySchema)
	if err != nil {
		return err
	}
	incidentProcessor := incidents.NewProcessor(database, incidents.NewStore(database), similarity.Processor{Provider: similarity.RuleBasedScorer{}, Validator: similarityValidator}, similarityValidator, incidentPolicy)
	incidentConsumer := incidents.NewConsumer(streamClient, incidentProcessor, outbox.ReportEventsStream, "signa-incident-processing", consumerName, cfg.AIPollInterval)
	evidencePolicyProcessor, err := incidents.NewEvidencePolicyProcessor(database, confidence.Policy{EmergingMin: confidencePolicyConfig.EmergingMinEvidence, CorroboratedMin: confidencePolicyConfig.CorroboratedMinEvidence, HighMin: confidencePolicyConfig.HighMinEvidence}, coordinationSyncWindow)
	if err != nil {
		return err
	}
	evidencePolicyConsumer := incidents.NewEvidencePolicyConsumer(streamClient, evidencePolicyProcessor, outbox.IncidentEventsStream, "signa-incident-confidence", consumerName, cfg.AIPollInterval).WithLogger(logger)
	lifecycleProcessor, err := incidents.NewLifecycleProcessor(database, lifecycle.Policy{
		ResolvingAfter: lifecyclePolicyConfig.ResolvingAfter,
		ResolvedAfter:  lifecyclePolicyConfig.ResolvedAfter,
		ExpiredAfter:   lifecyclePolicyConfig.ExpiredAfter,
	})
	if err != nil {
		return err
	}
	lifecycleConsumer := incidents.NewLifecycleConsumer(streamClient, lifecycleProcessor, outbox.IncidentEventsStream, "signa-incident-lifecycle", consumerName, cfg.AIPollInterval).WithLogger(logger)
	errCh := make(chan error, 6)
	go func() { errCh <- worker.Run(ctx, logger, cfg.WorkerInterval, publisher) }()
	go func() { errCh <- consumer.Run(ctx) }()
	go func() { errCh <- incidentConsumer.Run(ctx) }()
	go func() { errCh <- evidencePolicyConsumer.Run(ctx) }()
	go func() { errCh <- lifecycleConsumer.Run(ctx) }()
	go func() { errCh <- lifecycleProcessor.RunSweep(ctx, lifecyclePolicyConfig.SweepInterval, logger) }()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}
