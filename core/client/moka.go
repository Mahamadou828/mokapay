package client

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	compliancev1 "mokapay.app/proto/codebase/compliance/v1"
	filev1 "mokapay.app/proto/codebase/file/v1"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

type Connection string

const (
	ConnectionUser       Connection = "user"
	ConnectionPayment    Connection = "payment"
	ConnectionFile       Connection = "file"
	ConnectionCompliance Connection = "compliance"
)

type Config struct {
	UserServiceAddr       string
	PaymentServiceAddr    string
	FileServiceAddr       string
	ComplianceServiceAddr string
}

type User struct {
	conn         *grpc.ClientConn
	Auth         userv1.AuthServiceClient
	Admin        userv1.AdminServiceClient
	User         userv1.UserServiceClient
	Notification userv1.NotificationServiceClient
}

// Payment bundles the payment-service clients: the agent-profile
// lifecycle (Admin), the ledger workflow (Ledger), the client transfer
// surface (Transfer) and the agent surface (Agent).
type Payment struct {
	conn     *grpc.ClientConn
	Admin    paymentv1.AdminServiceClient
	Ledger   paymentv1.LedgerServiceClient
	Transfer paymentv1.TransferServiceClient
	Agent    paymentv1.AgentServiceClient
}

// File bundles the file-service client (presign + confirm uploads).
type File struct {
	conn *grpc.ClientConn
	File filev1.FileServiceClient
}

// Compliance bundles the compliance-service clients: the staff review
// surface (Admin, mounted on the gateway's ops tier) and the internal rule
// engine (Compliance — system passport only, called by payment's job, never
// exposed on the gateway).
type Compliance struct {
	conn       *grpc.ClientConn
	Admin      compliancev1.AdminServiceClient
	Compliance compliancev1.ComplianceServiceClient
}

type MokaClient struct {
	User       *User
	Payment    *Payment
	File       *File
	Compliance *Compliance
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
			conn:     conn,
			Admin:    paymentv1.NewAdminServiceClient(conn),
			Ledger:   paymentv1.NewLedgerServiceClient(conn),
			Transfer: paymentv1.NewTransferServiceClient(conn),
			Agent:    paymentv1.NewAgentServiceClient(conn),
		}
	}

	if p == nil && requiredConnection[ConnectionPayment] {
		return nil, errors.New("payment connection required")
	}

	var f *File
	if config.FileServiceAddr != "" {
		conn, err := grpc.NewClient(
			config.FileServiceAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithChainUnaryInterceptor(
				UnaryClientInterceptor(),
			),
		)
		if err != nil {
			return nil, err
		}
		f = &File{
			conn: conn,
			File: filev1.NewFileServiceClient(conn),
		}
	}

	if f == nil && requiredConnection[ConnectionFile] {
		return nil, errors.New("file connection required")
	}

	var c *Compliance
	if config.ComplianceServiceAddr != "" {
		conn, err := grpc.NewClient(
			config.ComplianceServiceAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithChainUnaryInterceptor(
				UnaryClientInterceptor(),
			),
		)
		if err != nil {
			return nil, err
		}
		c = &Compliance{
			conn:       conn,
			Admin:      compliancev1.NewAdminServiceClient(conn),
			Compliance: compliancev1.NewComplianceServiceClient(conn),
		}
	}

	if c == nil && requiredConnection[ConnectionCompliance] {
		return nil, errors.New("compliance connection required")
	}

	return &MokaClient{User: u, Payment: p, File: f, Compliance: c}, nil
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
	if c.File != nil {
		if err := c.File.conn.Close(); err != nil {
			return err
		}
	}
	if c.Compliance != nil {
		if err := c.Compliance.conn.Close(); err != nil {
			return err
		}
	}

	return nil
}
