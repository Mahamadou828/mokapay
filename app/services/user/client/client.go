// Package client is the only package other services may import from the user
// service: a thin typed wrapper over its gRPC surface.
package client

import (
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

type Client struct {
	conn  *grpc.ClientConn
	Auth  userv1.AuthServiceClient
	User  userv1.UserServiceClient
	Admin userv1.AdminServiceClient
}

func New(target string) (Client, error) {
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return Client{}, fmt.Errorf("connecting to user service at %s: %w", target, err)
	}

	return Client{
		conn:  conn,
		Auth:  userv1.NewAuthServiceClient(conn),
		User:  userv1.NewUserServiceClient(conn),
		Admin: userv1.NewAdminServiceClient(conn),
	}, nil
}

func (c Client) Close() error {
	return c.conn.Close()
}
