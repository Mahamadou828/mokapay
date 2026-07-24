package internal

import (
	"context"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	fileapi "mokapay.app/app/services/file/internal/api/file"
	"mokapay.app/app/services/file/internal/business/file/usecase"
	"mokapay.app/app/services/file/internal/integration/events"
	"mokapay.app/core/aws"
	"mokapay.app/core/db"
	coreweb "mokapay.app/core/http"
	"mokapay.app/core/logger"
	filev1 "mokapay.app/proto/codebase/file/v1"
)

const serviceName = "file"

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
	// Init integrations: object storage (MinIO locally, S3 later) and the
	// event publisher (log stand-in until the River integration lands).
	storage, err := aws.NewS3(ctx, cfg.S3)
	if err != nil {
		log.Error(ctx, "startup", "failed to init s3 client", "error", err)
		return err
	}
	publisher := events.NewLog(log)

	// -------------------------------------------------------------------------
	// Init usecase
	uc := usecase.New(log, pg, storage, publisher, usecase.Config{
		Bucket:       cfg.S3.Bucket,
		UploadTTL:    cfg.Upload.URLTTL,
		DownloadTTL:  cfg.Upload.DownloadTTL,
		MaxSizeBytes: cfg.Upload.MaxSizeBytes,
	})

	// -------------------------------------------------------------------------
	// Init gRPC server & register services
	// TODO(Mahamadou): add the passport server interceptor here once the
	// metadata wiring lands, and start the River workers (DeliverEvent,
	// orphan sweep via uc.SweepExpired).
	srv := grpc.NewServer()
	reflection.Register(srv)

	filev1.RegisterFileServiceServer(srv, fileapi.NewHandler(log, uc))

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
