package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/onerandomd3v/signa/internal/retention"
)

// LoadRetentionPolicy requires every retention boundary to be explicit. An
// unset value disables the retention worker rather than selecting a hidden
// product-policy default.
func LoadRetentionPolicy() (retention.Policy, error) {
	values := []struct {
		name string
		to   *time.Duration
	}{
		{"SIGNA_RETENTION_REPORT_EXACT_LOCATION", new(time.Duration)},
		{"SIGNA_RETENTION_USER_LOCATION", new(time.Duration)},
		{"SIGNA_RETENTION_MEDIA_METADATA", new(time.Duration)},
		{"SIGNA_RETENTION_OPERATIONAL_LOGS", new(time.Duration)},
		{"SIGNA_RETENTION_AUTH_SESSIONS", new(time.Duration)},
		{"SIGNA_RETENTION_SWEEP_INTERVAL", new(time.Duration)},
	}
	for _, value := range values {
		parsed, err := requiredRetentionDuration(value.name)
		if err != nil {
			return retention.Policy{}, err
		}
		*value.to = parsed
	}
	batchSize, err := requiredRetentionBatchSize()
	if err != nil {
		return retention.Policy{}, err
	}
	policy := retention.Policy{
		ReportExactLocation: *values[0].to,
		UserLocation:        *values[1].to,
		MediaMetadata:       *values[2].to,
		OperationalLogs:     *values[3].to,
		AuthSessions:        *values[4].to,
		SweepInterval:       *values[5].to,
		BatchSize:           batchSize,
	}
	if err := policy.Validate(); err != nil {
		return retention.Policy{}, err
	}
	return policy, nil
}

func requiredRetentionDuration(name string) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return 0, fmt.Errorf("%s is required to enable retention", name)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a duration greater than zero", name)
	}
	return parsed, nil
}

func requiredRetentionBatchSize() (int, error) {
	value := os.Getenv("SIGNA_RETENTION_BATCH_SIZE")
	if value == "" {
		return 0, fmt.Errorf("SIGNA_RETENTION_BATCH_SIZE is required to enable retention")
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > 10_000 {
		return 0, fmt.Errorf("SIGNA_RETENTION_BATCH_SIZE must be between 1 and 10000")
	}
	return parsed, nil
}
