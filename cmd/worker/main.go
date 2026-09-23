package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/logging"
	"github.com/onerandomd3v/signa/internal/worker"
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

	return worker.Run(ctx, logger, cfg.WorkerInterval)
}
