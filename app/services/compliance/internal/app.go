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
	adminapi "mokapay.app/app/services/compliance/internal/api/admin"
	evaluationapi "mokapay.app/app/services/compliance/internal/api/evaluation"
	evaluc "mokapay.app/app/services/compliance/internal/business/evaluation/usecase"
	investiguc "mokapay.app/app/services/compliance/internal/business/investigation/usecase"
	"mokapay.app/core/client"
	"mokapay.app/core/db"
	"mokapay.app/core/grpc/middleware"
	coreweb "mokapay.app/core/http"
	"mokapay.app/core/logger"
	compliancev1 "mokapay.app/proto/codebase/compliance/v1"
)

const serviceName = "compliance"

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
	// Init database. Compliance holds no downstream client: the rule
	// engine is fed a metrics snapshot on the request and never reads back
	// into payment or user.
	pg := db.New(cfg.DB)
	if err := pg.Open(); err != nil {
		log.Error(ctx, "startup", "failed to open database", "error", err)
		return err
	}
	defer pg.Close()

	// -------------------------------------------------------------------------
	// Init usecases
	evaluationUC := evaluc.New(log, pg, evaluc.Config{
		DailyLimitUSD:                 cfg.AML.DailyLimitUSD,
		WeeklyLimitUSD:                cfg.AML.WeeklyLimitUSD,
		StructuringMinCount:           cfg.AML.StructuringMinCount,
		SenderSpikeMult:               cfg.AML.SenderSpikeMult,
		AgentSpikeMult:                cfg.AML.AgentSpikeMult,
		BeneficiaryConcentrationCount: cfg.AML.BeneficiaryConcentrationCount,
		HourlyVelocityCount:           cfg.AML.HourlyVelocityCount,
		NighttimeCount:                cfg.AML.NighttimeCount,
		NewAccountDays:                cfg.AML.NewAccountDays,
		NewAccountVolumeUSD:           cfg.AML.NewAccountVolumeUSD,
		RiskMedium:                    cfg.AML.RiskMedium,
		RiskHigh:                      cfg.AML.RiskHigh,
		RiskCritical:                  cfg.AML.RiskCritical,
		PointsLow:                     cfg.AML.PointsLow,
		PointsMedium:                  cfg.AML.PointsMedium,
		PointsHigh:                    cfg.AML.PointsHigh,
	})
	investigationUC := investiguc.New(log, pg)

	// -------------------------------------------------------------------------
	// Init gRPC server & register services. The client interceptor rebuilds
	// the gateway-forwarded passport into the request context; handlers
	// police it themselves (ComplianceService is system-only and has no
	// gateway route at all).
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(
		middleware.PanicUnaryServerInterceptor(log.Error),
		middleware.LoggerUnaryServerInterceptor(log.Info),
		client.UnaryServerInterceptor(),
	))
	reflection.Register(srv)

	compliancev1.RegisterComplianceServiceServer(srv, evaluationapi.NewHandler(log, evaluationUC))
	compliancev1.RegisterAdminServiceServer(srv, adminapi.NewHandler(log, evaluationUC, investigationUC))

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
