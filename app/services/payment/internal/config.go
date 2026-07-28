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
		FileGRPCPort string
	}
	Redis struct {
		Host string
		Port string
	}
	FX struct {
		ProviderURL string        // empty = static rate only (local dev)
		StaticRate  int64         // CDF per USD in micros — the fallback
		CacheTTL    time.Duration // the display-vs-locked drift bound
		HTTPTimeout time.Duration
	}
	Transfer struct {
		FeeSameBps       int32 // same-currency fee, basis points
		FeeFXBps         int32 // cross-currency fee
		MarginBps        int32 // FX margin, platform-kept
		MaxAmount        int64 // from_amount cap, minor units
		MaxAgents        int32 // offers per broadcast round
		OfferTTL         time.Duration
		MaxRounds        int32
		MatchDeadline    time.Duration
		PaymentTimeout   time.Duration
		AutoConfirmAfter time.Duration
	}
	Deposit struct {
		// ChannelsJSON is the official receiving-account catalogue (JSON
		// array), parsed at startup by ledger.ParseDepositChannels. These
		// strings are where agents send real money — served from config,
		// never hardcoded client-side.
		ChannelsJSON string
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
	cfg.Client.FileGRPCPort = config.GetEnv("FILE_GRPC_PORT", "localhost:50052")

	cfg.Redis.Host = config.GetEnv("REDIS_HOST", "localhost")
	cfg.Redis.Port = config.GetEnv("REDIS_PORT", "6379")

	// Empty URL = static rate only; the static rate always exists as the
	// provider fallback (2830 CDF/USD unless overridden).
	cfg.FX.ProviderURL = config.GetEnv("FX_PROVIDER_URL", "")
	cfg.FX.StaticRate = int64(config.GetEnvAsInt("FX_STATIC_RATE_MICROS", 2_830_000_000))
	cfg.FX.CacheTTL = config.GetEnvAsTimeDuration("FX_CACHE_TTL", 5*time.Minute)
	cfg.FX.HTTPTimeout = config.GetEnvAsTimeDuration("FX_HTTP_TIMEOUT", 5*time.Second)

	cfg.Transfer.FeeSameBps = int32(config.GetEnvAsInt("FEE_SAME_BPS", 150))
	cfg.Transfer.FeeFXBps = int32(config.GetEnvAsInt("FEE_FX_BPS", 200))
	cfg.Transfer.MarginBps = int32(config.GetEnvAsInt("FX_MARGIN_BPS", 50))
	cfg.Transfer.MaxAmount = int64(config.GetEnvAsInt("TRANSFER_MAX_AMOUNT", 1_000_000_000))
	cfg.Transfer.MaxAgents = int32(config.GetEnvAsInt("MATCH_MAX_AGENTS", 5))
	cfg.Transfer.OfferTTL = config.GetEnvAsTimeDuration("OFFER_TTL", 2*time.Minute)
	cfg.Transfer.MaxRounds = int32(config.GetEnvAsInt("MATCH_MAX_ROUNDS", 10))
	cfg.Transfer.MatchDeadline = config.GetEnvAsTimeDuration("MATCH_DEADLINE", 30*time.Minute)
	cfg.Transfer.PaymentTimeout = config.GetEnvAsTimeDuration("PAYMENT_TIMEOUT", 24*time.Hour)
	cfg.Transfer.AutoConfirmAfter = config.GetEnvAsTimeDuration("AUTO_CONFIRM_AFTER", 24*time.Hour)

	// Default: the one account that exists today (design G2). Add entries
	// as real receiving accounts open — the client renders whatever the
	// list returns.
	cfg.Deposit.ChannelsJSON = config.GetEnv("DEPOSIT_CHANNELS",
		`[{"name":"M-Pesa","kind":"mobile_money","account_name":"MOKA PAY SARL","account_number":"+243815000000"}]`)

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
