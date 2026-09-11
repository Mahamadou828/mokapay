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
	accountapi "mokapay.app/app/services/user/internal/api/account"
	adminapi "mokapay.app/app/services/user/internal/api/admin"
	authapi "mokapay.app/app/services/user/internal/api/auth"
	notificationapi "mokapay.app/app/services/user/internal/api/notification"
	accountUC "mokapay.app/app/services/user/internal/business/auth/usecase/account"
	adminUC "mokapay.app/app/services/user/internal/business/auth/usecase/admin"
	authUC "mokapay.app/app/services/user/internal/business/auth/usecase/auth"
	enrollmentuc "mokapay.app/app/services/user/internal/business/enrollment/usecase"
	notificationuc "mokapay.app/app/services/user/internal/business/notification/usecase"
	"mokapay.app/app/services/user/internal/integration/email"
	"mokapay.app/app/services/user/internal/integration/webpush"
	"mokapay.app/core/aws"
	"mokapay.app/core/cache"
	"mokapay.app/core/client"
	"mokapay.app/core/db"
	"mokapay.app/core/grpc/middleware"
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
	// Init integrations: Cognito owns credentials & sessions, the configured
	// SMS provider delivers the OTP.
	cognito, err := aws.NewCognito(ctx, cfg.Cognito.ClientID, cfg.Cognito.ClientSecret, cfg.Cognito.PoolID, cfg.Cognito.Region)
	if err != nil {
		log.Error(ctx, "startup", "failed to init cognito client", "error", err)
		return err
	}

	// SMS sender: the provider SMS_PROVIDER selects, or the logging stand-in
	// when its credentials are absent. See newSMSSender.
	sender, err := newSMSSender(ctx, log, cfg)
	if err != nil {
		log.Error(ctx, "startup", "failed to init sms sender", "error", err)
		return err
	}

	// Email sender: the OTP channel for accounts that picked it. Real SMTP
	// once a host is configured, logging stand-in without one (local dev and
	// CI need no mail server). Unlike SMS there is no test-phone router —
	// SendOTP keeps the seeded phones on the SMS path outright.
	var mailer authUC.EmailSender = email.NewLog(log)
	if cfg.SMTP.Host != "" {
		smtp, err := email.NewSMTP(log, cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.User, cfg.SMTP.Password, cfg.SMTP.From)
		if err != nil {
			log.Error(ctx, "startup", "failed to init smtp sender", "error", err)
			return err
		}
		mailer = smtp
		log.Info(ctx, "startup", "status", "smtp email sender enabled", "host", cfg.SMTP.Host, "from", cfg.SMTP.From)
	} else {
		log.Info(ctx, "startup", "status", "no smtp host, otp emails are logged only")
	}

	// -------------------------------------------------------------------------
	// Redis backs the OTP rate limiter; unreachable redis fails open (the
	// limiter protects SMS cost — the DB attempt cap holds the security
	// floor), it never blocks startup.
	var redis *cache.Cache
	if c, err := cache.New(ctx, cache.Config{Host: cfg.Redis.Host, Port: cfg.Redis.Port}); err != nil {
		log.Error(ctx, "startup", "redis unreachable, otp rate limiting disabled", "error", err)
	} else {
		redis = c
		defer func() {
			if err := redis.Close(); err != nil {
				log.Error(ctx, "shutdown", "failed to close redis", "error", err)
			}
		}()
	}
	limiter := authUC.NewLimiter(log, redis, authUC.LimiterConfig{
		SendCooldown: cfg.Auth.OtpSendCooldown,
		HourlyLimit:  cfg.Auth.OtpHourlyLimit,
		VerifyLimit:  cfg.Auth.OtpVerifyLimit,
		VerifyWindow: cfg.Auth.OtpVerifyWindow,
	})

	// -------------------------------------------------------------------------
	// Init usecases
	authUsecase, err := authUC.New(log, pg, sender, mailer, cognito, cfg.Auth.OtpPepper, limiter)
	if err != nil {
		log.Error(ctx, "startup", "failed to init auth usecase", "error", err)
		return err
	}
	accountUsecase := accountUC.New(log, pg)
	adminUsecase := adminUC.New(log, pg, cognito)

	// Push sender: real web push with VAPID keys, logging stand-in without
	// (local dev needs no keys).
	var pusher notificationuc.PushSender = webpush.NewLog(log)
	if cfg.Push.VapidPublicKey != "" && cfg.Push.VapidPrivateKey != "" {
		pusher = webpush.NewSender(cfg.Push.VapidPublicKey, cfg.Push.VapidPrivateKey, cfg.Push.Subscriber, cfg.Push.TTL)
	}
	notificationUC := notificationuc.New(log, pg, pusher, cfg.Push.VapidPublicKey)

	// The enrollment workflow notifies candidates through the notification
	// usecase directly (same service, no gRPC hop) and reaches the payment
	// service for the guarantee/profile side of the flow.
	moka, err := client.NewMokaClient(client.Config{
		PaymentServiceAddr: cfg.Client.PaymentGRPCPort,
	}, map[client.Connection]bool{client.ConnectionPayment: true})
	if err != nil {
		log.Error(ctx, "startup", "failed to init moka client", "error", err)
		return err
	}
	defer func() {
		if err := moka.Close(ctx); err != nil {
			log.Error(ctx, "shutdown", "failed to close moka client", "error", err)
		}
	}()
	enrollmentUC := enrollmentuc.New(log, pg, notificationUC, moka, cfg.Enrollment.TOSVersion)

	// -------------------------------------------------------------------------
	// Init gRPC server & register services. The client interceptor rebuilds
	// the gateway-forwarded passport into the request context; handlers
	// needing an identity read it with passport.FromContext.
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(
		middleware.PanicUnaryServerInterceptor(log.Error),
		middleware.LoggerUnaryServerInterceptor(log.Info),
		client.UnaryServerInterceptor(),
	))
	reflection.Register(srv)

	userv1.RegisterAuthServiceServer(srv, authapi.NewHandler(log, authUsecase))
	userv1.RegisterUserServiceServer(srv, accountapi.NewUserHandler(log, accountUsecase, enrollmentUC))
	userv1.RegisterAdminServiceServer(srv, adminapi.NewHandler(log, adminUsecase, enrollmentUC))
	userv1.RegisterNotificationServiceServer(srv, notificationapi.NewHandler(log, notificationUC))

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
