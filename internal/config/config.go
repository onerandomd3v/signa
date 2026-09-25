package config

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"time"
)

const (
	defaultAPIAddr                      = ":8080"
	defaultDatabaseURL                  = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	defaultRedisAddr                    = "localhost:6379"
	defaultShutdownTimeout              = 10 * time.Second
	defaultWorkerInterval               = 500 * time.Millisecond
	defaultReportRatePerMinute          = 6
	defaultReportRateBurst              = 3
	defaultGlobalReportRatePerMinute    = 120
	defaultGlobalReportRateBurst        = 30
	defaultOpenAIModel                  = "gpt-4.1-mini"
	defaultOpenAIBaseURL                = "https://api.openai.com/v1"
	defaultAISchemaPath                 = "contracts/ai/extraction/v0/schema.json"
	defaultAIGenerationSchemaPath       = "contracts/ai/extraction/v0/openai.schema.json"
	defaultAIConsumerGroup              = "signa-ai-text-extraction"
	defaultAIPollInterval               = time.Second
	PublicIncidentGeometryPolicyVersion = "signa.public-incident-geometry.v1"
)

// Config contains the runtime settings needed by the foundation processes.
type Config struct {
	APIAddr                   string
	DatabaseURL               string
	RedisAddr                 string
	ShutdownTimeout           time.Duration
	WorkerInterval            time.Duration
	ReportRatePerMinute       int
	ReportRateBurst           int
	GlobalReportRatePerMinute int
	GlobalReportRateBurst     int
	OpenAIAPIKey              string
	OpenAIModel               string
	OpenAIBaseURL             string
	AISchemaPath              string
	AIGenerationSchemaPath    string
	AIConsumerGroup           string
	AIConsumerName            string
	AIPollInterval            time.Duration
	WebAllowedOrigins         []string
	ObjectStorageEndpoint     string
	ObjectStorageRegion       string
	ObjectStorageBucket       string
	ObjectStorageAccessKeyID  string
	ObjectStorageSecret       string
}

type IncidentPolicy struct {
	CandidateRadiusMeters  float64
	CandidateTimeWindow    time.Duration
	CandidateLimit         int
	SimilarityThreshold    float64
	SimilarityWinnerMargin float64
}

type ConfidencePolicy struct {
	EmergingMinEvidence     int
	CorroboratedMinEvidence int
	HighMinEvidence         int
}

type IncidentLifecyclePolicy struct {
	ResolvingAfter time.Duration
	ResolvedAfter  time.Duration
	ExpiredAfter   time.Duration
	SweepInterval  time.Duration
}

type PublicIncidentGeometryPolicy struct {
	Version         string
	GridMeters      float64
	MinRadiusMeters float64
	SimplifyMeters  float64
}

func LoadPublicIncidentGeometryPolicy() (PublicIncidentGeometryPolicy, error) {
	grid, err := requiredPositiveFloat("SIGNA_PUBLIC_INCIDENT_GRID_METERS")
	if err != nil {
		return PublicIncidentGeometryPolicy{}, err
	}
	minRadius, err := requiredPositiveFloat("SIGNA_PUBLIC_INCIDENT_MIN_RADIUS_METERS")
	if err != nil {
		return PublicIncidentGeometryPolicy{}, err
	}
	simplify, err := requiredPositiveFloat("SIGNA_PUBLIC_INCIDENT_SIMPLIFY_METERS")
	if err != nil {
		return PublicIncidentGeometryPolicy{}, err
	}
	return PublicIncidentGeometryPolicy{Version: PublicIncidentGeometryPolicyVersion, GridMeters: grid, MinRadiusMeters: minRadius, SimplifyMeters: simplify}, nil
}

func (p PublicIncidentGeometryPolicy) Validate() error {
	if p.Version != PublicIncidentGeometryPolicyVersion {
		return fmt.Errorf("public incident geometry policy version must be %q", PublicIncidentGeometryPolicyVersion)
	}
	for name, value := range map[string]float64{"grid meters": p.GridMeters, "minimum radius": p.MinRadiusMeters, "simplify meters": p.SimplifyMeters} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
			return fmt.Errorf("public incident geometry %s must be finite and greater than zero", name)
		}
	}
	return nil
}

func LoadIncidentPolicy() (IncidentPolicy, error) {
	radius, err := requiredFloat("SIGNA_INCIDENT_CANDIDATE_RADIUS_METERS", func(v float64) bool { return v > 0 })
	if err != nil {
		return IncidentPolicy{}, err
	}
	window, err := requiredDuration("SIGNA_INCIDENT_CANDIDATE_TIME_WINDOW")
	if err != nil {
		return IncidentPolicy{}, err
	}
	limit, err := requiredIntRange("SIGNA_INCIDENT_CANDIDATE_LIMIT", 1, 100)
	if err != nil {
		return IncidentPolicy{}, err
	}
	threshold, err := requiredFloat("SIGNA_INCIDENT_SIMILARITY_THRESHOLD", func(v float64) bool { return v >= 0 && v <= 1 })
	if err != nil {
		return IncidentPolicy{}, err
	}
	margin, err := requiredFloat("SIGNA_INCIDENT_SIMILARITY_WINNER_MARGIN", func(v float64) bool { return v >= 0 && v <= 1 })
	if err != nil {
		return IncidentPolicy{}, err
	}
	return IncidentPolicy{radius, window, limit, threshold, margin}, nil
}

func LoadConfidencePolicy() (ConfidencePolicy, error) {
	emerging, err := requiredIntRange("SIGNA_CONFIDENCE_EMERGING_MIN_EVIDENCE", 1, int(^uint(0)>>1))
	if err != nil {
		return ConfidencePolicy{}, err
	}
	corroborated, err := requiredIntRange("SIGNA_CONFIDENCE_CORROBORATED_MIN_EVIDENCE", 1, int(^uint(0)>>1))
	if err != nil {
		return ConfidencePolicy{}, err
	}
	high, err := requiredIntRange("SIGNA_CONFIDENCE_HIGH_MIN_EVIDENCE", 1, int(^uint(0)>>1))
	if err != nil {
		return ConfidencePolicy{}, err
	}
	policy := ConfidencePolicy{EmergingMinEvidence: emerging, CorroboratedMinEvidence: corroborated, HighMinEvidence: high}
	if emerging >= corroborated || corroborated >= high {
		return ConfidencePolicy{}, fmt.Errorf("confidence evidence thresholds must be strictly increasing")
	}
	return policy, nil
}

func LoadCoordinationSyncWindow() (time.Duration, error) {
	value := os.Getenv("SIGNA_COORDINATION_SYNC_WINDOW")
	if value == "" {
		return 0, fmt.Errorf("SIGNA_COORDINATION_SYNC_WINDOW is required for incident processing")
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < time.Second {
		return 0, fmt.Errorf("SIGNA_COORDINATION_SYNC_WINDOW must be a duration of at least one second")
	}
	return parsed, nil
}

func LoadIncidentLifecyclePolicy() (IncidentLifecyclePolicy, error) {
	resolvingAfter, err := requiredDuration("SIGNA_INCIDENT_RESOLVING_AFTER")
	if err != nil {
		return IncidentLifecyclePolicy{}, err
	}
	resolvedAfter, err := requiredDuration("SIGNA_INCIDENT_RESOLVED_AFTER")
	if err != nil {
		return IncidentLifecyclePolicy{}, err
	}
	expiredAfter, err := requiredDuration("SIGNA_INCIDENT_EXPIRED_AFTER")
	if err != nil {
		return IncidentLifecyclePolicy{}, err
	}
	sweepInterval, err := requiredDuration("SIGNA_INCIDENT_LIFECYCLE_SWEEP_INTERVAL")
	if err != nil {
		return IncidentLifecyclePolicy{}, err
	}
	if resolvingAfter >= resolvedAfter || resolvedAfter >= expiredAfter {
		return IncidentLifecyclePolicy{}, fmt.Errorf("incident lifecycle durations must be strictly increasing")
	}
	return IncidentLifecyclePolicy{resolvingAfter, resolvedAfter, expiredAfter, sweepInterval}, nil
}

func requiredFloat(name string, valid func(float64) bool) (float64, error) {
	value := os.Getenv(name)
	if value == "" {
		return 0, fmt.Errorf("%s is required for incident processing", name)
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || !valid(parsed) {
		return 0, fmt.Errorf("%s must be finite and satisfy its configured range", name)
	}
	return parsed, nil
}

func requiredPositiveFloat(name string) (float64, error) {
	value, err := requiredFloat(name, func(value float64) bool { return value > 0 })
	if err != nil {
		return 0, fmt.Errorf("%s must be finite and greater than zero", name)
	}
	return value, nil
}

func requiredDuration(name string) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return 0, fmt.Errorf("%s is required for incident processing", name)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a duration greater than zero", name)
	}
	return parsed, nil
}

func requiredIntRange(name string, min, max int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return 0, fmt.Errorf("%s is required for incident processing", name)
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < min || parsed > max {
		return 0, fmt.Errorf("%s must be between %d and %d", name, min, max)
	}
	return parsed, nil
}

// Load reads configuration from environment variables and applies local defaults.
func Load() (Config, error) {
	shutdownTimeout, err := durationFromEnv("SIGNA_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}
	workerInterval, err := durationFromEnv("SIGNA_WORKER_INTERVAL", defaultWorkerInterval)
	if err != nil {
		return Config{}, err
	}
	reportRatePerMinute, err := intFromEnv("SIGNA_REPORT_RATE_PER_MINUTE", defaultReportRatePerMinute)
	if err != nil {
		return Config{}, err
	}
	reportRateBurst, err := intFromEnv("SIGNA_REPORT_RATE_BURST", defaultReportRateBurst)
	if err != nil {
		return Config{}, err
	}
	globalReportRatePerMinute, err := intFromEnv("SIGNA_REPORT_GLOBAL_RATE_PER_MINUTE", defaultGlobalReportRatePerMinute)
	if err != nil {
		return Config{}, err
	}
	globalReportRateBurst, err := intFromEnv("SIGNA_REPORT_GLOBAL_RATE_BURST", defaultGlobalReportRateBurst)
	if err != nil {
		return Config{}, err
	}
	aiPollInterval, err := durationFromEnv("SIGNA_AI_POLL_INTERVAL", defaultAIPollInterval)
	if err != nil {
		return Config{}, err
	}
	webAllowedOrigins, err := webAllowedOriginsFromEnv()
	if err != nil {
		return Config{}, err
	}

	apiAddr := os.Getenv("SIGNA_API_ADDR")
	if apiAddr == "" {
		apiAddr = defaultAPIAddr
	}
	databaseURL := os.Getenv("SIGNA_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultDatabaseURL
	}
	redisAddr := os.Getenv("SIGNA_REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = defaultRedisAddr
	}
	openAIModel := os.Getenv("SIGNA_OPENAI_MODEL")
	if openAIModel == "" {
		openAIModel = defaultOpenAIModel
	}
	openAIBaseURL := os.Getenv("SIGNA_OPENAI_BASE_URL")
	if openAIBaseURL == "" {
		openAIBaseURL = defaultOpenAIBaseURL
	}
	aiSchemaPath := os.Getenv("SIGNA_AI_SCHEMA_PATH")
	if aiSchemaPath == "" {
		aiSchemaPath = defaultAISchemaPath
	}
	aiGenerationSchemaPath := os.Getenv("SIGNA_AI_GENERATION_SCHEMA_PATH")
	if aiGenerationSchemaPath == "" {
		aiGenerationSchemaPath = defaultAIGenerationSchemaPath
	}
	aiConsumerGroup := os.Getenv("SIGNA_AI_CONSUMER_GROUP")
	if aiConsumerGroup == "" {
		aiConsumerGroup = defaultAIConsumerGroup
	}

	return Config{
		APIAddr:                   apiAddr,
		DatabaseURL:               databaseURL,
		RedisAddr:                 redisAddr,
		ShutdownTimeout:           shutdownTimeout,
		WorkerInterval:            workerInterval,
		ReportRatePerMinute:       reportRatePerMinute,
		ReportRateBurst:           reportRateBurst,
		GlobalReportRatePerMinute: globalReportRatePerMinute,
		GlobalReportRateBurst:     globalReportRateBurst,
		OpenAIAPIKey:              os.Getenv("SIGNA_OPENAI_API_KEY"),
		OpenAIModel:               openAIModel,
		OpenAIBaseURL:             openAIBaseURL,
		AISchemaPath:              aiSchemaPath,
		AIGenerationSchemaPath:    aiGenerationSchemaPath,
		AIConsumerGroup:           aiConsumerGroup,
		AIConsumerName:            os.Getenv("SIGNA_AI_CONSUMER_NAME"),
		AIPollInterval:            aiPollInterval,
		WebAllowedOrigins:         webAllowedOrigins,
		ObjectStorageEndpoint:     os.Getenv("SIGNA_OBJECT_STORAGE_ENDPOINT"),
		ObjectStorageRegion:       os.Getenv("SIGNA_OBJECT_STORAGE_REGION"),
		ObjectStorageBucket:       os.Getenv("SIGNA_OBJECT_STORAGE_BUCKET"),
		ObjectStorageAccessKeyID:  os.Getenv("SIGNA_OBJECT_STORAGE_ACCESS_KEY_ID"),
		ObjectStorageSecret:       os.Getenv("SIGNA_OBJECT_STORAGE_SECRET_ACCESS_KEY"),
	}, nil
}

func intFromEnv(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func durationFromEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}
	return duration, nil
}

type PriorityPolicy struct {
	P1RadiusMeters float64
	P2RadiusMeters float64
	LocationMaxAge time.Duration
	IncidentMaxAge time.Duration
}

func LoadPriorityPolicy() (PriorityPolicy, error) {
	p1Radius, err := requiredFloat("SIGNA_PRIORITY_P1_RADIUS_METERS", func(v float64) bool { return v > 0 })
	if err != nil {
		return PriorityPolicy{}, err
	}
	p2Radius, err := requiredFloat("SIGNA_PRIORITY_P2_RADIUS_METERS", func(v float64) bool { return v > 0 })
	if err != nil {
		return PriorityPolicy{}, err
	}
	locationMaxAge, err := requiredDuration("SIGNA_PRIORITY_LOCATION_MAX_AGE")
	if err != nil {
		return PriorityPolicy{}, err
	}
	incidentMaxAge, err := requiredDuration("SIGNA_PRIORITY_INCIDENT_MAX_AGE")
	if err != nil {
		return PriorityPolicy{}, err
	}
	if p1Radius >= p2Radius {
		return PriorityPolicy{}, fmt.Errorf("priority radii must satisfy P1 < P2")
	}
	return PriorityPolicy{P1RadiusMeters: p1Radius, P2RadiusMeters: p2Radius, LocationMaxAge: locationMaxAge, IncidentMaxAge: incidentMaxAge}, nil
}
