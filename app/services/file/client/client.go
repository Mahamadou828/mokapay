// Package client is the only package other services may import from the
// file service: a thin typed wrapper over its gRPC surface.
package client

import (
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	filev1 "mokapay.app/proto/codebase/file/v1"
)

type Client struct {
	conn *grpc.ClientConn
	File filev1.FileServiceClient
}

func New(target string) (Client, error) {
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return Client{}, fmt.Errorf("connecting to file service at %s: %w", target, err)
	}

	return Client{
		conn: conn,
		File: filev1.NewFileServiceClient(conn),
	}, nil
}

func (c Client) Close() error {
	return c.conn.Close()
}
