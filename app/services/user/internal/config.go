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
		OtpPepper       string // HMAC key for OTP code hashes
		OtpTTL          time.Duration
		OtpMaxAttempts  int
		OtpSendCooldown time.Duration
		OtpHourlyLimit  int
	}
	Cognito struct {
		ClientID     string
		ClientSecret string
		PoolID       string
		Region       string
	}
	Enrollment struct {
		// TOSVersion is the current terms version; an accept carrying a
		// different one is rejected (the client displayed stale terms).
		TOSVersion string
	}
	Push struct {
		VapidPublicKey  string
		VapidPrivateKey string
		Subscriber      string        // mailto: contact, VAPID sub claim
		TTL             time.Duration // how long the push service holds an undelivered message
	}
	Seed struct {
		OpsPhone string
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

	cfg.Auth.OtpPepper = config.GetEnv("OTP_PEPPER", "dev-pepper-change-me")
	cfg.Auth.OtpTTL = config.GetEnvAsTimeDuration("OTP_TTL", 10*time.Minute)
	cfg.Auth.OtpMaxAttempts = config.GetEnvAsInt("OTP_MAX_ATTEMPTS", 3)
	cfg.Auth.OtpSendCooldown = config.GetEnvAsTimeDuration("OTP_SEND_COOLDOWN", time.Minute)
	cfg.Auth.OtpHourlyLimit = config.GetEnvAsInt("OTP_HOURLY_LIMIT", 5)

	cfg.Enrollment.TOSVersion = config.GetEnv("TOS_VERSION", "1.0")

	// Empty VAPID keys switch the service to the logging push sender.
	cfg.Push.VapidPublicKey = config.GetEnv("VAPID_PUBLIC_KEY", "")
	cfg.Push.VapidPrivateKey = config.GetEnv("VAPID_PRIVATE_KEY", "")
	cfg.Push.Subscriber = config.GetEnv("VAPID_SUBSCRIBER", "")
	cfg.Push.TTL = config.GetEnvAsTimeDuration("PUSH_TTL", 30*time.Second)

	cfg.Cognito.ClientID = config.GetEnv("COGNITO_CLIENT_ID", "")
	cfg.Cognito.ClientSecret = config.GetEnv("COGNITO_CLIENT_SECRET", "")
	cfg.Cognito.PoolID = config.GetEnv("COGNITO_POOL_ID", "")
	cfg.Cognito.Region = config.GetEnv("COGNITO_REGION", "eu-west-1")

	cfg.Seed.OpsPhone = config.GetEnv("SEED_OPS_PHONE", "")

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
