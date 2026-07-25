package internal

import (
	"context"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"mokapay.app/app/services/gateway/internal/api"
	appMiddleware "mokapay.app/app/services/gateway/internal/middleware"
	"mokapay.app/core/aws"
	"mokapay.app/core/client"
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
		Client: struct {
			UserGRPCPort    string
			PaymentGRPCPort string
			FileGRPCPort    string
		}{
			UserGRPCPort:    config.GetEnv("USER_GRPC_PORT", "localhost:50051"),
			PaymentGRPCPort: config.GetEnv("PAYMENT_GRPC_PORT", "localhost:50053"),
			FileGRPCPort:    config.GetEnv("FILE_GRPC_PORT", "localhost:50052"),
		},
	}
	cfg.Cognito.ClientID = config.GetEnv("COGNITO_CLIENT_ID", "")
	cfg.Cognito.ClientSecret = config.GetEnv("COGNITO_CLIENT_SECRET", "")
	cfg.Cognito.PoolID = config.GetEnv("COGNITO_POOL_ID", "")
	cfg.Cognito.Region = config.GetEnv("COGNITO_REGION", "eu-west-1")
	// -----------------------------------------------------------------------------------------------------------------
	// Init logger
	log := newLogger()

	// -------------------------------------------------------------------------
	// GOMAXPROCS
	log.Info(ctx, "startup", "GOMAXPROCS", runtime.GOMAXPROCS(0), "build", build)

	// -------------------------------------------------------------------------
	// init cognito client
	cognito, err := aws.NewCognito(ctx, cfg.Cognito.ClientID, cfg.Cognito.ClientSecret, cfg.Cognito.PoolID, cfg.Cognito.Region)
	if err != nil {
		log.Error(ctx, "startup", "failed to init cognito client", "error", err)
		return err
	}

	// -------------------------------------------------------------------------
	// init moka client
	moka, err := client.NewMokaClient(client.Config{
		UserServiceAddr:    cfg.Client.UserGRPCPort,
		PaymentServiceAddr: cfg.Client.PaymentGRPCPort,
		FileServiceAddr:    cfg.Client.FileGRPCPort,
	}, map[client.Connection]bool{
		client.ConnectionUser:    true,
		client.ConnectionPayment: true,
		client.ConnectionFile:    true,
	})
	if err != nil {
		log.Error(ctx, "startup", "failed to init cognito client", "error", err)
		return err
	}

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
	// Init HTTP server. Auth is NOT global — it is applied per route tier in
	// api.Register (public auth flows are anonymous by design).
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
	closer, err := api.Register(api.Config{
		Moka: moka,
		Auth: appMiddleware.Auth(cognito, moka),
	}, log, server)
	if err != nil {
		log.Error(ctx, "failed to register handlers", "error", err)
		return err
	}

	defer closer(ctx)
	defer moka.Close(ctx)
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
