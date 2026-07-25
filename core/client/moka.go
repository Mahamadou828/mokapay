package client

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

type Connection string

const (
	ConnectionUser    Connection = "user"
	ConnectionPayment Connection = "payment"
)

type Config struct {
	UserServiceAddr    string
	PaymentServiceAddr string
	FileServiceAddr    string
}

type User struct {
	conn         *grpc.ClientConn
	Auth         userv1.AuthServiceClient
	Admin        userv1.AdminServiceClient
	User         userv1.UserServiceClient
	Notification userv1.NotificationServiceClient
}

// Payment bundles the payment-service clients: the agent-profile
// lifecycle (Admin) and the ledger workflow (Ledger).
type Payment struct {
	conn   *grpc.ClientConn
	Admin  paymentv1.AdminServiceClient
	Ledger paymentv1.LedgerServiceClient
}

type MokaClient struct {
	User    *User
	Payment *Payment
}

func NewMokaClient(config Config, requiredConnection map[Connection]bool) (*MokaClient, error) {
	var u *User
	if config.UserServiceAddr != "" {
		conn, err := grpc.NewClient(
			config.UserServiceAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithChainUnaryInterceptor(
				UnaryClientInterceptor(),
			),
		)
		if err != nil {
			return nil, err
		}
		u = &User{
			conn:         conn,
			Auth:         userv1.NewAuthServiceClient(conn),
			Admin:        userv1.NewAdminServiceClient(conn),
			User:         userv1.NewUserServiceClient(conn),
			Notification: userv1.NewNotificationServiceClient(conn),
		}
	}

	if u == nil && requiredConnection[ConnectionUser] {
		return nil, errors.New("user connection required")
	}

	var p *Payment
	if config.PaymentServiceAddr != "" {
		conn, err := grpc.NewClient(
			config.PaymentServiceAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithChainUnaryInterceptor(
				UnaryClientInterceptor(),
			),
		)
		if err != nil {
			return nil, err
		}
		p = &Payment{
			conn:   conn,
			Admin:  paymentv1.NewAdminServiceClient(conn),
			Ledger: paymentv1.NewLedgerServiceClient(conn),
		}
	}

	if p == nil && requiredConnection[ConnectionPayment] {
		return nil, errors.New("payment connection required")
	}

	return &MokaClient{User: u, Payment: p}, nil
}

func (c *MokaClient) Close(ctx context.Context) error {
	if c.User != nil {
		if err := c.User.conn.Close(); err != nil {
			return err
		}
	}
	if c.Payment != nil {
		if err := c.Payment.conn.Close(); err != nil {
			return err
		}
	}

	return nil
}
