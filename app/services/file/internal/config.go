package internal

import (
	"context"
	"os"
	"time"

	"mokapay.app/core/aws"
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
	S3     aws.S3Config
	Upload struct {
		URLTTL       time.Duration // presigned PUT lifetime
		DownloadTTL  time.Duration // presigned GET lifetime
		MaxSizeBytes int64
	}
}

// newConfig resolves the whole service config from the environment. Defaults
// target local development (compose infra on localhost).
func newConfig() Config {
	var cfg Config

	// 50052/3012: the user service holds 50051/3011 during local dev.
	cfg.GRPC.Port = config.GetEnv("GRPC_PORT", ":50052")
	cfg.GRPC.DebugPort = config.GetEnv("DEBUG_PORT", ":3012")
	cfg.GRPC.ShutdownTimeout = config.GetEnvAsTimeDuration("SHUTDOWN_TIMEOUT", 20*time.Second)

	cfg.DB = db.PostgresConfig{
		Host:     config.GetEnv("DB_HOST", "localhost"),
		Port:     config.GetEnv("DB_PORT", "5433"),
		Username: config.GetEnv("DB_USER", "mokapay"),
		Password: config.GetEnv("DB_PASSWORD", "mokapay"),
		Database: config.GetEnv("DB_NAME", "files"),
	}

	endpoint := config.GetEnv("S3_ENDPOINT", "http://localhost:9000")
	cfg.S3 = aws.S3Config{
		Endpoint:  endpoint,
		Region:    config.GetEnv("S3_REGION", "us-east-1"),
		Bucket:    config.GetEnv("S3_BUCKET", "mokapay-files"),
		AccessKey: config.GetEnv("S3_ACCESS_KEY", "mokapay"),
		SecretKey: config.GetEnv("S3_SECRET_KEY", "mokapay1234"),
		// Path-style goes with a custom endpoint (MinIO); real S3 (empty
		// endpoint) uses virtual-hosted style.
		UsePathStyle: endpoint != "",
	}

	cfg.Upload.URLTTL = config.GetEnvAsTimeDuration("UPLOAD_URL_TTL", 15*time.Minute)
	cfg.Upload.DownloadTTL = config.GetEnvAsTimeDuration("DOWNLOAD_URL_TTL", time.Hour)
	cfg.Upload.MaxSizeBytes = int64(config.GetEnvAsInt("MAX_FILE_SIZE_BYTES", 10*1024*1024))

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
