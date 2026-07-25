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
	DB     db.PostgresConfig
	Server struct {
		MaxEventWorkers int
	}
	Client struct {
		UserGRPCPort string
	}
}

// newConfig resolves the whole service config from the environment. Defaults
// target local development (compose infra on localhost).
func newConfig() Config {
	var cfg Config

	// 50053/3013: user holds 50051/3011 and file 50052/3012 during local dev.
	cfg.GRPC.Port = config.GetEnv("GRPC_PORT", ":50053")
	cfg.GRPC.DebugPort = config.GetEnv("DEBUG_PORT", ":3013")
	cfg.GRPC.ShutdownTimeout = config.GetEnvAsTimeDuration("SHUTDOWN_TIMEOUT", 20*time.Second)

	cfg.DB = db.PostgresConfig{
		Host:     config.GetEnv("DB_HOST", "localhost"),
		Port:     config.GetEnv("DB_PORT", "5433"),
		Username: config.GetEnv("DB_USER", "mokapay"),
		Password: config.GetEnv("DB_PASSWORD", "mokapay"),
		Database: config.GetEnv("DB_NAME", "payments"),
	}

	cfg.Server.MaxEventWorkers = config.GetEnvAsInt("MAX_EVENT_WORKERS", 10)

	cfg.Client.UserGRPCPort = config.GetEnv("USER_GRPC_PORT", "localhost:50051")

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
