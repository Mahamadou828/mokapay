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
	fileapi "mokapay.app/app/services/file/internal/api/file"
	"mokapay.app/app/services/file/internal/business/file/events"
	"mokapay.app/app/services/file/internal/business/file/usecase"
	"mokapay.app/core/aws"
	"mokapay.app/core/client"
	"mokapay.app/core/db"
	"mokapay.app/core/grpc/middleware"
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

	// -------------------------------------------------------------------------
	// Init the Moka client: the upload worker fans the file.upload event
	// out to its consumers (payment proof gating, user notifications).
	moka, err := client.NewMokaClient(client.Config{
		UserServiceAddr:    cfg.Client.UserGRPCPort,
		PaymentServiceAddr: cfg.Client.PaymentGRPCPort,
	}, map[client.Connection]bool{
		client.ConnectionUser:    true,
		client.ConnectionPayment: true,
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
	// register river workers
	workers := river.NewWorkers()
	river.AddWorker(workers, &events.UploadWorker{
		Moka:   *moka,
		Logger: log,
	})

	// The sweep worker needs the usecase, which needs the river client —
	// register it now, inject the usecase below, before Start.
	sweepWorker := &events.SweepWorker{Logger: log}
	river.AddWorker(workers, sweepWorker)

	// init river client
	riverClient, err := river.NewClient(riverpgxv5.New(pg.PgxPool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: cfg.Server.MaxEventWorkers},
		},
		Workers: workers,
		PeriodicJobs: []*river.PeriodicJob{
			// The orphan sweep: cancels pending uploads whose presign TTL
			// elapsed (and deletes any half-uploaded object).
			river.NewPeriodicJob(
				river.PeriodicInterval(15*time.Minute),
				func() (river.JobArgs, *river.InsertOpts) {
					return events.SweepArg{}, nil
				},
				nil,
			),
		},
	})
	if err != nil {
		log.Error(ctx, "startup", "failed to init river client", "error", err)
		return err
	}

	// -------------------------------------------------------------------------
	// Init usecase
	event := events.NewPublisher(riverClient)
	uc := usecase.New(log, pg, storage, event, usecase.Config{
		Bucket:       cfg.S3.Bucket,
		UploadTTL:    cfg.Upload.URLTTL,
		DownloadTTL:  cfg.Upload.DownloadTTL,
		MaxSizeBytes: cfg.Upload.MaxSizeBytes,
	})

	// Close the worker↔usecase loop, then start picking jobs up. Without
	// Start the workers never run — inserts alone only queue them.
	sweepWorker.UC = uc
	if err := riverClient.Start(ctx); err != nil {
		log.Error(ctx, "startup", "failed to start river client", "error", err)
		return err
	}
	defer func() {
		if err := riverClient.Stop(ctx); err != nil {
			log.Error(ctx, "shutdown", "failed to stop river client", "error", err)
		}
	}()

	// Init gRPC server & register services. The client interceptor rebuilds
	// the gateway-forwarded passport into the request context.
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(
		middleware.PanicUnaryServerInterceptor(log.Error),
		middleware.LoggerUnaryServerInterceptor(log.Info),
		client.UnaryServerInterceptor(),
	))
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

		if err := riverClient.Stop(ctx); err != nil {
			log.Error(ctx, "failed to gracefully stop river client", "error", err)
		}
	}

	return nil
}
