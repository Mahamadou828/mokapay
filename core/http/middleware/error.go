package middleware

import (
	"context"
	"errors"
	"net/http"
	"path"

	errs "mokapay.app/core/error"
	core "mokapay.app/core/http"
	"mokapay.app/core/otel"
)

func Errors(log core.Logger) core.MidFunc {
	m := func(next core.HandlerFunc) core.HandlerFunc {
		h := func(ctx context.Context, r *http.Request) core.Encoder {
			res := next(ctx, r)

			err := checkIsError(res)
			if err == nil {
				return res
			}

			_, span := otel.AddSpan(ctx, "app.sdk.mid.error")
			span.RecordError(err)
			defer span.End()

			var appErr *errs.Error
			if !errors.As(err, &appErr) {
				appErr = errs.Errorf(errInternal, "Internal Server Error")
			}

			log(ctx, "handled error during request",
				"err", err,
				"source_err_file", path.Base(appErr.FileName),
				"source_err_func", path.Base(appErr.FuncName),
			)

			if appErr.Code == errInternalOnlyLog {
				appErr = errs.Errorf(errInternal, "Internal Server Error")
			}

			// Send the error to the web package so the error can be
			// used as the response.
			return appErr
		}

		return h
	}

	return m
}
