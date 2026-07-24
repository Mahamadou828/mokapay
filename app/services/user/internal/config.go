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
	DB    db.PostgresConfig
	Redis struct {
		Host string
		Port string
	}
	Auth struct {
		JWTSecret       string
		AccessTokenTTL  time.Duration
		RefreshTokenTTL time.Duration
		OtpTTL          time.Duration
		OtpMaxAttempts  int
		OtpSendCooldown time.Duration
		OtpHourlyLimit  int
	}
	Seed struct {
		OpsPhone    string
		OpsPassword string
	}
}

// newConfig resolves the whole service config from the environment. Defaults
// target local development (compose infra on localhost).
func newConfig() Config {
	var cfg Config

	cfg.GRPC.Port = config.GetEnv("GRPC_PORT", ":50051")
	cfg.GRPC.DebugPort = config.GetEnv("DEBUG_PORT", ":3011")
	cfg.GRPC.ShutdownTimeout = config.GetEnvAsTimeDuration("SHUTDOWN_TIMEOUT", 20*time.Second)

	cfg.DB = db.PostgresConfig{
		Host:     config.GetEnv("DB_HOST", "localhost"),
		Port:     config.GetEnv("DB_PORT", "5433"),
		Username: config.GetEnv("DB_USER", "mokapay"),
		Password: config.GetEnv("DB_PASSWORD", "mokapay"),
		Database: config.GetEnv("DB_NAME", "users"),
	}

	cfg.Redis.Host = config.GetEnv("REDIS_HOST", "localhost")
	cfg.Redis.Port = config.GetEnv("REDIS_PORT", "6379")

	cfg.Auth.JWTSecret = config.GetEnv("JWT_SECRET", "dev-secret-change-me")
	cfg.Auth.AccessTokenTTL = config.GetEnvAsTimeDuration("ACCESS_TOKEN_TTL", 15*time.Minute)
	cfg.Auth.RefreshTokenTTL = config.GetEnvAsTimeDuration("REFRESH_TOKEN_TTL", 720*time.Hour)
	cfg.Auth.OtpTTL = config.GetEnvAsTimeDuration("OTP_TTL", 10*time.Minute)
	cfg.Auth.OtpMaxAttempts = config.GetEnvAsInt("OTP_MAX_ATTEMPTS", 3)
	cfg.Auth.OtpSendCooldown = config.GetEnvAsTimeDuration("OTP_SEND_COOLDOWN", time.Minute)
	cfg.Auth.OtpHourlyLimit = config.GetEnvAsInt("OTP_HOURLY_LIMIT", 5)

	cfg.Seed.OpsPhone = config.GetEnv("SEED_OPS_PHONE", "")
	cfg.Seed.OpsPassword = config.GetEnv("SEED_OPS_PASSWORD", "")

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
