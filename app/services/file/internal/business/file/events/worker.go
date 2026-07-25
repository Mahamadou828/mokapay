package events

import (
	"context"

	"github.com/riverqueue/river"
	"mokapay.app/app/services/file/internal/business/file"
	"mokapay.app/core/passport"
)

type UploadArg struct {
	File file.File `json:"file"`
}

func (a UploadArg) Kind() string {
	return "file.upload"
}

type UploadWorker struct {
	river.WorkerDefaults[UploadArg]
}

func (w *UploadWorker) Work(ctx context.Context, job *river.Job[UploadArg]) error {
	pass := passport.SystemPassport()
	ctx = passport.AddToContext(ctx, pass)

	// send request base on the file purpose
	switch job.Args.File.Purpose {

	}

	return nil
}
