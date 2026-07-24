package internal

import (
	"context"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	accountapi "mokapay.app/app/services/user/internal/api/account"
	adminapi "mokapay.app/app/services/user/internal/api/admin"
	authapi "mokapay.app/app/services/user/internal/api/auth"
	"mokapay.app/app/services/user/internal/business/auth/usecase"
	"mokapay.app/app/services/user/internal/integration/sms"
	"mokapay.app/core/aws"
	"mokapay.app/core/db"
	coreweb "mokapay.app/core/http"
	"mokapay.app/core/logger"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

const serviceName = "user"

var build = "develop"

func Run() error {
	ctx := context.Background()

	// -------------------------------------------------------------------------
	// Configuration
	cfg := newConfig()

	// -------------------------------------------------------------------------
	// Init logger
	log := newLogger()

	// -------------------------------------------------------------------------
	// GOMAXPROCS
	log.Info(ctx, "startup", "GOMAXPROCS", runtime.GOMAXPROCS(0), "build", build)

	// -------------------------------------------------------------------------
	// Init Debug Service
	debug, err := coreweb.NewDebugServer(
		logger.NewStdLogger(log, logger.LevelDebug),
		cfg.GRPC.DebugPort,
	)
	if err != nil {
		log.Error(ctx, "startup", "failed to init debug server", "error", err)
		return err
	}

	// -------------------------------------------------------------------------
	// Init database
	pg := db.New(cfg.DB)
	if err := pg.Open(); err != nil {
		log.Error(ctx, "startup", "failed to open database", "error", err)
		return err
	}
	defer pg.Close()

	// -------------------------------------------------------------------------
	// Init integrations: Cognito owns credentials & sessions, SMS is the
	// logging mock until a provider is picked.
	cognito, err := aws.NewCognito(ctx, cfg.Cognito.ClientID, cfg.Cognito.ClientSecret, cfg.Cognito.PoolID, cfg.Cognito.Region)
	if err != nil {
		log.Error(ctx, "startup", "failed to init cognito client", "error", err)
		return err
	}
	sender := sms.NewLog(log)

	// -------------------------------------------------------------------------
	// Init usecases
	authUC, err := usecase.New(log, pg, sender, cognito, cfg.Auth.OtpPepper)
	if err != nil {
		log.Error(ctx, "startup", "failed to init auth usecase", "error", err)
		return err
	}
	accountUC := usecase.NewAccount(log, pg, cognito)

	// -------------------------------------------------------------------------
	// Init gRPC server & register services
	srv := grpc.NewServer()
	reflection.Register(srv)

	recovery.UnaryServerInterceptor()

	userv1.RegisterAuthServiceServer(srv, authapi.NewHandler(log, authUC))
	userv1.RegisterUserServiceServer(srv, accountapi.NewUserHandler(log, accountUC))
	userv1.RegisterAdminServiceServer(srv, adminapi.NewHandler(log, accountUC))

	lis, err := net.Listen("tcp", cfg.GRPC.Port)
	if err != nil {
		log.Error(ctx, "startup", "failed to listen", "port", cfg.GRPC.Port, "error", err)
		return err
	}

	// -----------------------------------------------------------------------------------------------------------------
	// StartUp and Shutdown
	shutdown, serverErrors := make(chan os.Signal, 1), make(chan error, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, syscall.SIGKILL)

	// start debug
	go func() {
		log.Info(ctx, "startup", "status", "debug router started", "host", cfg.GRPC.DebugPort)
		serverErrors <- debug.ListenAndServe()
	}()

	// start gRPC server
	go func() {
		log.Info(ctx, "startup", "status", "grpc server started", "host", cfg.GRPC.Port)
		serverErrors <- srv.Serve(lis)
	}()

	// listen
	select {
	case err := <-serverErrors:
		log.Error(ctx, "startup", "error", err)
		return err
	case sig := <-shutdown:
		log.Info(ctx, "shutdown", "status", "shutdown started", "signal", sig)
		defer log.Info(ctx, "shutdown", "status", "shutdown complete", "signal", sig)

		ctx, cancel := context.WithTimeout(ctx, cfg.GRPC.ShutdownTimeout)
		defer cancel()

		// GracefulStop drains in-flight RPCs; force-stop if the deadline passes.
		stopped := make(chan struct{})
		go func() {
			srv.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(cfg.GRPC.ShutdownTimeout):
			srv.Stop()
		}

		if err := debug.Shutdown(ctx); err != nil {
			log.Error(ctx, "failed to gracefully shutdown debug server", "error", err)
		}
	}

	return nil
}
