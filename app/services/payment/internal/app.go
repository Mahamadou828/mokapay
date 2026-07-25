package internal

import (
	"context"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	adminapi "mokapay.app/app/services/payment/internal/api/admin"
	ledgerapi "mokapay.app/app/services/payment/internal/api/ledger"
	profileuc "mokapay.app/app/services/payment/internal/business/agentprofile/usecase"
	"mokapay.app/app/services/payment/internal/business/ledger/events"
	ledgeruc "mokapay.app/app/services/payment/internal/business/ledger/usecase"
	"mokapay.app/core/client"
	"mokapay.app/core/db"
	"mokapay.app/core/grpc/middleware"
	coreweb "mokapay.app/core/http"
	"mokapay.app/core/logger"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

const serviceName = "payment"

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
	// Init the Moka client: the River notify worker pushes through the
	// user service's NotificationService under a system passport.
	moka, err := client.NewMokaClient(client.Config{
		UserServiceAddr: cfg.Client.UserGRPCPort,
	}, map[client.Connection]bool{
		client.ConnectionUser: true,
	})
	if err != nil {
		log.Error(ctx, "startup", "failed to init moka client", "error", err)
		return err
	}
	defer func() {
		if err := moka.Close(ctx); err != nil {
			log.Error(ctx, "shutdown", "failed to close moka client", "error", err)
		}
	}()

	// -------------------------------------------------------------------------
	// River: the notify worker + the client the publisher enqueues through.
	workers := river.NewWorkers()
	river.AddWorker(workers, &events.LedgerNotifyWorker{
		Moka:   moka,
		Logger: log,
	})

	riverClient, err := river.NewClient(riverpgxv5.New(pg.PgxPool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: cfg.Server.MaxEventWorkers},
		},
		Workers: workers,
	})
	if err != nil {
		log.Error(ctx, "startup", "failed to init river client", "error", err)
		return err
	}
	// Without Start the workers never pick jobs up — inserts alone only
	// queue them.
	if err := riverClient.Start(ctx); err != nil {
		log.Error(ctx, "startup", "failed to start river client", "error", err)
		return err
	}
	defer func() {
		if err := riverClient.Stop(ctx); err != nil {
			log.Error(ctx, "shutdown", "failed to stop river client", "error", err)
		}
	}()

	notification := events.NewLedgerNotification(log, riverClient, moka)

	// -------------------------------------------------------------------------
	// Init usecases
	profileUC := profileuc.New(log, pg)
	ledgerUC := ledgeruc.New(log, pg, notification)

	// -------------------------------------------------------------------------
	// Init gRPC server & register services. The client interceptor rebuilds
	// the gateway-forwarded passport into the request context; handlers
	// police it themselves (internal RPCs — no gateway tier in front).
	// TODO: register TransferService with the transfers volley.
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(
		middleware.PanicUnaryServerInterceptor(log.Error),
		middleware.LoggerUnaryServerInterceptor(log.Info),
		client.UnaryServerInterceptor(),
	))
	reflection.Register(srv)

	paymentv1.RegisterAdminServiceServer(srv, adminapi.NewHandler(log, profileUC))
	paymentv1.RegisterLedgerServiceServer(srv, ledgerapi.NewHandler(log, ledgerUC))

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
