package internal

import (
	"context"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"mokapay.app/app/services/gateway/internal/api"
	"mokapay.app/core/config"
	coreweb "mokapay.app/core/http"
	"mokapay.app/core/http/middleware"
	"mokapay.app/core/logger"
)

/*
TODO: DISABLE REQUEST LOGGER ON PROD - ONLY DEV, STAGING, LOCAL
*/
const serviceName = "gateway"

var build = "develop"

func Run() error {
	ctx := context.Background()

	// -------------------------------------------------------------------------
	// Configuration
	cfg := Config{
		Web: struct {
			APIHost            string
			DebugPort          string
			ShutdownTimeout    time.Duration
			RequestTimeout     time.Duration
			CORSAllowedOrigins []string
		}{
			DebugPort:          config.GetEnv("DEBUG_PORT", ":3010"),
			ShutdownTimeout:    config.GetEnvAsTimeDuration("SHUTDOWN_TIMEOUT", 20*time.Second),
			RequestTimeout:     config.GetEnvAsTimeDuration("REQUEST_TIMEOUT", 10*time.Second),
			APIHost:            config.GetEnv("API_HOST", ":8080"),
			CORSAllowedOrigins: config.GetEnvAsSlice("CORS_ALLOWED_ORIGINS", []string{"*"}),
		},
	}
	// -----------------------------------------------------------------------------------------------------------------
	// Init logger
	log := newLogger()

	// -------------------------------------------------------------------------
	// GOMAXPROCS
	log.Info(ctx, "startup", "GOMAXPROCS", runtime.GOMAXPROCS(0), "build", build)

	// -------------------------------------------------------------------------
	// Init Debug Service
	debug, err := coreweb.NewDebugServer(
		logger.NewStdLogger(log, logger.LevelDebug),
		cfg.Web.DebugPort,
	)
	if err != nil {
		log.Error(ctx, "startup", "failed to init debug server", "error", err)
		return err
	}

	// -------------------------------------------------------------------------
	// Init HTTP server
	server := coreweb.NewServer(
		log.Info,
		nil,
		middleware.Errors(log.Error),
		middleware.Logger(log.Info),
		middleware.Panics(log.Error),
	)
	server.WithCORS(cfg.Web.CORSAllowedOrigins)
	server.WithOptions(coreweb.Opts{
		RequestTimeout:  cfg.Web.RequestTimeout,
		ShutdownTimeout: cfg.Web.ShutdownTimeout,
	})

	// Handlers
	closer, err := api.Register(api.Config{}, log, server)
	if err != nil {
		log.Error(ctx, "failed to register handlers", "error", err)
		return err
	}

	defer closer(ctx)
	// Create Server

	// -----------------------------------------------------------------------------------------------------------------
	// StartUp and Shutdown
	shutdown, serverErrors := make(chan os.Signal, 1), make(chan error, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, syscall.SIGKILL)

	// start debug
	go func() {
		log.Info(ctx, "startup", "status", "debug router started", "host", cfg.Web.DebugPort)
		serverErrors <- debug.ListenAndServe()
	}()

	// start http server
	go func() {
		log.Info(ctx, "startup", "status", "server started", "host", cfg.Web.APIHost)
		serverErrors <- server.ListenAndServe(cfg.Web.APIHost)
	}()

	// listen
	select {
	case err := <-serverErrors:
		log.Error(ctx, "startup", "error", err)
		return err
	case sig := <-shutdown:
		log.Info(ctx, "shutdown", "status", "shutdown started", "signal", sig)
		defer log.Info(ctx, "shutdown", "status", "shutdown complete", "signal", sig)

		ctx, cancel := context.WithTimeout(ctx, cfg.Web.ShutdownTimeout)
		defer cancel()
		if err := debug.Shutdown(ctx); err != nil {
			log.Error(ctx, "failed to gracefully shutdown debug server", "error", err)
		}
		if err := server.Shutdown(ctx); err != nil {
			log.Error(ctx, "failed to shutdown server", "error", err)
		}
	}

	return nil
}
