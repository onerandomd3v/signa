package logging

import (
	"log/slog"
	"os"
)

// New returns the process logger used by the API and worker foundation.
func New() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
