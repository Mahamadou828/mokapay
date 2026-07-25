package client

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

type Connection string

const (
	ConnectionUser Connection = "user"
)

type Config struct {
	UserServiceAddr    string
	PaymentServiceAddr string
	FileServiceAddr    string
}

type User struct {
	conn  *grpc.ClientConn
	Auth  userv1.AuthServiceClient
	Admin userv1.AdminServiceClient
	User  userv1.UserServiceClient
}

type MokaClient struct {
	User *User
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
			conn:  conn,
			Auth:  userv1.NewAuthServiceClient(conn),
			Admin: userv1.NewAdminServiceClient(conn),
			User:  userv1.NewUserServiceClient(conn),
		}
	}

	if u == nil && requiredConnection[ConnectionUser] {
		return nil, errors.New("user connection required")
	}

	return &MokaClient{User: u}, nil
}

func (c *MokaClient) Close(ctx context.Context) error {
	if c.User != nil {
		if err := c.User.conn.Close(); err != nil {
			return err
		}
	}

	return nil
}
