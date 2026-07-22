// Package http - middleware provide definition for middleware function and also a set of default middleware
package http

type MidFunc func(h HandlerFunc) HandlerFunc

// wrapMiddleware creates a new handler by wrapping middleware around a final
// handler. The middlewares' Handlers will be executed by requests in the order
// they are provided.
func wrapMiddleware(mw []MidFunc, h HandlerFunc) HandlerFunc {
	// Loop backwards through the middleware invoking each one. Replace the
	// handler with the new wrapped handler. Looping backwards ensures that the
	// first middleware of the slice is the first to be executed by requests.
	for i := len(mw) - 1; i >= 0; i-- {
		mwFunc := mw[i]
		if mwFunc != nil {
			h = mwFunc(h)
		}
	}

	return h
}
