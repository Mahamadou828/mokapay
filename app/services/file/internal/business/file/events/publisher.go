package events

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"mokapay.app/app/services/file/internal/business/file"
)

type Publisher struct {
	riverClient *river.Client[pgx.Tx]
}

func NewPublisher(r *river.Client[pgx.Tx]) Publisher {
	return Publisher{
		riverClient: r,
	}
}

func (p Publisher) FileUploaded(ctx context.Context, tx pgx.Tx, f file.File) error {
	if _, err := p.riverClient.InsertTx(ctx, tx, UploadArg{File: f}, &river.InsertOpts{}); err != nil {
		return err
	}
	return nil
}
