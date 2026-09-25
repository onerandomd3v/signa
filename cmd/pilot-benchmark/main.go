package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	pilotevaluation "github.com/onerandomd3v/signa/internal/ai/pilot/evaluation"
)

func main() {
	root, err := findRepositoryRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pilot-benchmark:", err)
		os.Exit(2)
	}
	report, err := pilotevaluation.Run(context.Background(), root, pilotevaluation.Providers{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "pilot-benchmark:", err)
		os.Exit(2)
	}
	data, err := report.Marshal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pilot-benchmark: encode report:", err)
		os.Exit(2)
	}
	if _, err := os.Stdout.Write(append(data, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, "pilot-benchmark: write report:", err)
		os.Exit(2)
	}
	if reportHasFailures(report) {
		os.Exit(1)
	}
}

func findRepositoryRoot() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		if fileExists(filepath.Join(directory, "go.mod")) && fileExists(filepath.Join(directory, "contracts/ai/pilot-benchmark/v1/evaluation.json")) {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", errors.New("run this command inside the Signa repository")
		}
		directory = parent
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func reportHasFailures(report pilotevaluation.Report) bool {
	if report.Overall.FailedCases > 0 {
		return true
	}
	for _, component := range report.Guardrails {
		if component.FailedCases > 0 {
			return true
		}
	}
	return false
}
