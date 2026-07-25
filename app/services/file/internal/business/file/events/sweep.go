package events

import (
	"context"

	"github.com/riverqueue/river"
	"mokapay.app/core/logger"
)

// Sweeper is the orphan-sweep entry the worker drives (satisfied by the
// file usecase — declared here to keep the import direction events →
// usecase-free).
type Sweeper interface {
	SweepExpired(ctx context.Context) (int, error)
}

// SweepArg is the periodic orphan sweep: cancels pending uploads whose
// presign TTL elapsed and removes any half-uploaded object.
type SweepArg struct{}

func (SweepArg) Kind() string { return "file.orphan_sweep" }

type SweepWorker struct {
	river.WorkerDefaults[SweepArg]
	UC     Sweeper
	Logger *logger.Logger
}

func (w *SweepWorker) Work(ctx context.Context, _ *river.Job[SweepArg]) error {
	swept, err := w.UC.SweepExpired(ctx)
	if err != nil {
		w.Logger.Error(ctx, "File.SweepWorker", "sweep failed", err)
		return err
	}
	if swept > 0 {
		w.Logger.Info(ctx, "File.SweepWorker", "orphaned uploads cancelled", swept)
	}
	return nil
}
