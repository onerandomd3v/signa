package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/latency"
)

type options struct {
	reportID *uuid.UUID
	limit    int
}

type timelineReader interface {
	TraceReport(context.Context, uuid.UUID) (latency.Timeline, error)
	Recent(context.Context, int) ([]latency.Timeline, error)
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	selected, err := parseOptions(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load Signa configuration: %w", err)
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return errors.New("create database connection from SIGNA_DATABASE_URL")
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return errors.New("database is unavailable; check SIGNA_DATABASE_URL and PostgreSQL readiness")
	}
	return writeResult(ctx, latency.NewStore(pool), selected, output)
}

func parseOptions(args []string) (options, error) {
	flags := flag.NewFlagSet("report-trace", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	reportIDValue := flags.String("report-id", "", "trace a single report UUID")
	limitValue := flags.Int("limit", 0, "show the newest 1..100 reports")
	if err := flags.Parse(args); err != nil {
		return options{}, fmt.Errorf("usage: report-trace (-report-id UUID | -limit 1..100)")
	}
	if flags.NArg() != 0 || (*reportIDValue == "") == (*limitValue == 0) {
		return options{}, errors.New("choose exactly one mode: -report-id UUID or -limit 1..100")
	}
	if *reportIDValue != "" {
		id, err := uuid.Parse(*reportIDValue)
		if err != nil || id == uuid.Nil {
			return options{}, errors.New("-report-id must be a non-zero UUID")
		}
		return options{reportID: &id}, nil
	}
	if *limitValue < 1 || *limitValue > latency.MaxRecentSample {
		return options{}, fmt.Errorf("-limit must be between 1 and %d", latency.MaxRecentSample)
	}
	return options{limit: *limitValue}, nil
}

func writeResult(ctx context.Context, reader timelineReader, selected options, output io.Writer) error {
	var value any
	if selected.reportID != nil {
		item, err := reader.TraceReport(ctx, *selected.reportID)
		if err != nil {
			return errors.New("report trace query failed")
		}
		value = item
	} else {
		items, err := reader.Recent(ctx, selected.limit)
		if err != nil {
			return errors.New("recent report trace query failed")
		}
		value = items
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("write report trace JSON: %w", err)
	}
	return nil
}
