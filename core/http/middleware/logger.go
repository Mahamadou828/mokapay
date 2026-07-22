package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	core "mokapay.app/core/http"
)

// Logger writes information about the request to the logs.
func Logger(log core.Logger) core.MidFunc {
	m := func(next core.HandlerFunc) core.HandlerFunc {
		h := func(ctx context.Context, r *http.Request) core.Encoder {
			now := time.Now()

			path := r.URL.Path
			if r.URL.RawQuery != "" {
				path = fmt.Sprintf("%s?%s", path, r.URL.RawQuery)
			}

			log(ctx, "request started", "method", r.Method, "path", path, "remoteaddr", r.RemoteAddr)

			res := next(ctx, r)
			var statusCode int

			if err := checkIsError(res); err != nil {
				statusCode = errInternal.HTTPStatus()

				var appError *core.Error
				if errors.As(err, &appError) {
					statusCode = appError.Code.HTTPStatus()
				}
			}

			log(ctx,
				"request completed",
				"method",
				r.Method, "path",
				path,
				"remoteaddr",
				r.RemoteAddr,
				"statuscode",
				statusCode,
				"since",
				time.Since(now).String(),
			)

			return res
		}

		return h
	}

	return m
}
