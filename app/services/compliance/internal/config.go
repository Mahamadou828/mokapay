package internal

import (
	"context"
	"os"
	"time"

	"mokapay.app/core/config"
	"mokapay.app/core/db"
	"mokapay.app/core/logger"
	"mokapay.app/core/otel"
)

// Config represent the application config
type Config struct {
	GRPC struct {
		Port            string
		DebugPort       string
		ShutdownTimeout time.Duration
	}
	DB db.PostgresConfig
	// AML holds every rule threshold. All of it is env config so ops can
	// retune a rule without a migration and without a release — no
	// threshold is hardcoded in a rule, a view or a procedure. Amounts are
	// WHOLE USD, matching the snapshot the caller sends.
	//
	// The defaults below are the prototype's values; they are NOT yet
	// validated against real regulatory requirements.
	AML struct {
		DailyLimitUSD                 int64
		WeeklyLimitUSD                int64
		StructuringMinCount           int32
		SenderSpikeMult               int32
		AgentSpikeMult                int32
		BeneficiaryConcentrationCount int32
		HourlyVelocityCount           int32
		NighttimeCount                int32
		NewAccountDays                int32
		NewAccountVolumeUSD           int64
		RiskMedium                    int64
		RiskHigh                      int64
		RiskCritical                  int64
		PointsLow                     int32
		PointsMedium                  int32
		PointsHigh                    int32
	}
}

// newConfig resolves the whole service config from the environment.
// Defaults target local development (compose infra on localhost).
func newConfig() Config {
	var cfg Config

	// 50054/3014: user holds 50051/3011, file 50052/3012 and payment
	// 50053/3013 during local dev.
	cfg.GRPC.Port = config.GetEnv("GRPC_PORT", ":50054")
	cfg.GRPC.DebugPort = config.GetEnv("DEBUG_PORT", ":3014")
	cfg.GRPC.ShutdownTimeout = config.GetEnvAsTimeDuration("SHUTDOWN_TIMEOUT", 20*time.Second)

	cfg.DB = db.PostgresConfig{
		Host:     config.GetEnv("DB_HOST", "localhost"),
		Port:     config.GetEnv("DB_PORT", "5433"),
		Username: config.GetEnv("DB_USER", "mokapay"),
		Password: config.GetEnv("DB_PASSWORD", "mokapay"),
		Database: config.GetEnv("DB_NAME", "compliance"),
	}

	cfg.AML.DailyLimitUSD = int64(config.GetEnvAsInt("AML_DAILY_LIMIT_USD", 10_000))
	cfg.AML.WeeklyLimitUSD = int64(config.GetEnvAsInt("AML_WEEKLY_LIMIT_USD", 10_000))
	cfg.AML.StructuringMinCount = int32(config.GetEnvAsInt("AML_STRUCTURING_MIN_COUNT", 3))
	cfg.AML.SenderSpikeMult = int32(config.GetEnvAsInt("AML_SENDER_SPIKE_MULT", 5))
	cfg.AML.AgentSpikeMult = int32(config.GetEnvAsInt("AML_AGENT_SPIKE_MULT", 3))
	cfg.AML.BeneficiaryConcentrationCount = int32(config.GetEnvAsInt("AML_BENEFICIARY_CONCENTRATION_COUNT", 10))
	cfg.AML.HourlyVelocityCount = int32(config.GetEnvAsInt("AML_HOURLY_VELOCITY_COUNT", 5))
	cfg.AML.NighttimeCount = int32(config.GetEnvAsInt("AML_NIGHTTIME_COUNT", 3))
	cfg.AML.NewAccountDays = int32(config.GetEnvAsInt("AML_NEW_ACCOUNT_DAYS", 1))
	cfg.AML.NewAccountVolumeUSD = int64(config.GetEnvAsInt("AML_NEW_ACCOUNT_VOLUME_USD", 500))

	cfg.AML.RiskMedium = int64(config.GetEnvAsInt("AML_RISK_MEDIUM", 30))
	cfg.AML.RiskHigh = int64(config.GetEnvAsInt("AML_RISK_HIGH", 60))
	cfg.AML.RiskCritical = int64(config.GetEnvAsInt("AML_RISK_CRITICAL", 80))

	cfg.AML.PointsLow = int32(config.GetEnvAsInt("AML_SEVERITY_LOW_PTS", 5))
	cfg.AML.PointsMedium = int32(config.GetEnvAsInt("AML_SEVERITY_MEDIUM_PTS", 15))
	cfg.AML.PointsHigh = int32(config.GetEnvAsInt("AML_SEVERITY_HIGH_PTS", 35))

	return cfg
}

// newLogger builds the structured logger used by every command, wiring the
// shared alert event so error logs are reported consistently across commands.
func newLogger() *logger.Logger {
	var log *logger.Logger
	events := logger.Events{
		Error: func(ctx context.Context, r logger.Record) {
			log.Info(ctx, "******* SEND ALERT *******")
		},
	}

	log = logger.NewWithEvents(os.Stdout, logger.LevelInfo, serviceName, otel.GetTraceID, events)
	return log
}
