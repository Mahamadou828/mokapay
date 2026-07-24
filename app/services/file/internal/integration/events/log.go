// Package events carries stored-file announcements to their consumers.
//
// Log is the v1 stand-in publisher: it only logs. TODO(Mahamadou): replace
// with the River-backed publisher — a DeliverEvent job inserted with
// InsertTx(tx, ...) so the event enqueue commits atomically with the
// confirm transaction, whose worker calls
// payment.v1.EventService.FileUploaded (see the platform-infra design doc).
package events

import (
	"context"
	"database/sql"

	"mokapay.app/app/services/file/internal/business/file"
	"mokapay.app/core/logger"
)

type Log struct {
	logger *logger.Logger
}

func NewLog(l *logger.Logger) Log {
	return Log{logger: l}
}

// FileUploaded implements usecase.Publisher. The tx is accepted (and
// ignored) so the real implementation can enlist in the confirm
// transaction without changing the interface.
func (l Log) FileUploaded(ctx context.Context, _ *sql.Tx, f file.File) error {
	l.logger.Info(ctx, "event",
		"type", "file.document.uploaded",
		"file_id", f.ID.String(),
		"purpose", string(f.Purpose),
		"owner_id", f.OwnerID.String(),
	)
	return nil
}
