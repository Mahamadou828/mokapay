// Package http provide a small http framework extension.
package http

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/encoding/protojson"
)

type Encoder interface {
	Encode() ([]byte, string, error)
}

// httpStatus lets an Encoder pick its response code (protocodec.Proto,
// core/error.Error, ... all expose Status() int).
type httpStatus interface {
	Status() int
}

type Logger func(ctx context.Context, msg string, args ...any)
type HandlerFunc func(ctx context.Context, r *http.Request) Encoder

// marshaler emits proto3 JSON using the .proto field names (snake_case) and
// string enum values, so the gateway's REST contract matches the protobuf
// definitions one-to-one. Unpopulated fields are emitted to keep response
// shapes stable for the frontend.
var marshaler = protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}

type Opts struct {
	RequestTimeout       time.Duration
	DisableRequestLogger bool
	ShutdownTimeout      time.Duration
}

type Server struct {
	log     Logger
	mux     *http.ServeMux
	otmux   http.Handler
	tracer  trace.Tracer
	mw      []MidFunc
	server  *http.Server
	origins []string
	options Opts
}

// NewServer create a new HTTP server
func NewServer(log Logger, tracer trace.Tracer, mw ...MidFunc) *Server {
	mux := http.NewServeMux()

	return &Server{
		log:    log,
		mux:    mux,
		tracer: tracer,
		mw:     mw,
		otmux:  otelhttp.NewHandler(mux, "request"),
	}
}

// NewGroup create a new mux group
func (s *Server) NewGroup(path string, mw ...MidFunc) *Group {
	if path[0] != '/' {
		path = fmt.Sprintf("/%s", path)
	}

	return &Group{
		register: s.RegisterHandler,
		path:     path,
		mw:       mw,
	}
}

// RegisterHandler attach a new handler for a given HTTP method and path
func (s *Server) RegisterHandler(method, path string, handler HandlerFunc, mw ...MidFunc) {
	s.mux.HandleFunc(
		fmt.Sprintf("%s %s", method, path),
		s.newHandler(handler, mw...),
	)
	return
}

// RegisterRawHandler mounts a stdlib handler as-is: no timeout context, no
// Encoder response pipeline, no middleware. It is the escape hatch for the
// protocols that pipeline cannot express — a websocket upgrade hijacks the
// connection (so nothing may write a response afterwards) and has to outlive
// the request timeout. Such a handler authenticates itself.
func (s *Server) RegisterRawHandler(method, path string, handler http.HandlerFunc) {
	s.mux.HandleFunc(fmt.Sprintf("%s %s", method, path), handler)
}

// FileServerReact starts a file server based on the specified file system and
// directory inside that file system for a statically built react webapp.
func (s *Server) FileServerReact(static embed.FS, dir string, path string) error {
	fileMatcher := regexp.MustCompile(`\.[a-zA-Z]*$`)

	fSys, err := fs.Sub(static, dir)
	if err != nil {
		return fmt.Errorf("switching to static folder: %w", err)
	}

	fileServer := http.StripPrefix(path, http.FileServer(http.FS(fSys)))

	h := func(w http.ResponseWriter, r *http.Request) {
		if !fileMatcher.MatchString(r.URL.Path) {
			p, err := static.ReadFile(fmt.Sprintf("%s/index.html", dir))
			if err != nil {
				s.log(r.Context(), "FileServerReact", "index.html not found", "ERROR", err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(p)
			return
		}

		fileServer.ServeHTTP(w, r)
	}

	s.mux.HandleFunc(fmt.Sprintf("GET %s", path), h)

	return nil
}

// FileServer starts a file server based on the specified file system and
// directory inside that file system.
func (s *Server) FileServer(static embed.FS, dir string, path string) error {
	fSys, err := fs.Sub(static, dir)
	if err != nil {
		return fmt.Errorf("switching to static folder: %w", err)
	}

	fileServer := http.StripPrefix(path, http.FileServer(http.FS(fSys)))

	s.mux.Handle(fmt.Sprintf("GET %s", path), fileServer)

	return nil
}

// WithCORS allow setting Access-Control-Allow-Origin header value
func (s *Server) WithCORS(o []string) {
	s.origins = o
}

func (s *Server) WithOptions(opts Opts) {
	s.options = opts
}

func (s *Server) ListenAndServe(addr string) error {
	s.server = &http.Server{
		Addr:    addr,
		Handler: s,
	}

	return s.server.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.server.Shutdown(ctx); err != nil {
		s.log(ctx, "shutdown", "failed to gracefully stop", err, "forcing server close")
		s.server.Close()
	}

	return nil
}

// ServeHTTP implements the http.Handler interface. It's the entry point for
// all http traffic and allows the opentelemetry mux to run first to handle
// tracing. The opentelemetry mux then calls the application mux to handle
// application traffic. This was set up in the NewApp function.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.origins != nil {
		reqOrigin := r.Header.Get("Origin")
		for _, origin := range s.origins {
			if origin == "*" || origin == reqOrigin {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				break
			}
		}

		w.Header().Set("Access-Control-Allow-Methods", "POST, PATCH, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		// Handle pre-flight by sending a 200 OK response.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")

	// Clean the request path to prevent http.ServeMux from issuing 301 redirects
	// for unclean paths (e.g. //v1/auth/register). Without this, clients following
	// the redirect switch to GET, causing 405 on POST/PUT/DELETE routes.
	r.URL.Path = path.Clean(r.URL.Path)

	s.otmux.ServeHTTP(w, r)
}

// newHandler create a new root handler, wrapping user request handler and handling error + response
func (s *Server) newHandler(h HandlerFunc, mw ...MidFunc) http.HandlerFunc {
	handler := wrapMiddleware(mw, h)
	handler = wrapMiddleware(s.mw, handler)

	return func(w http.ResponseWriter, r *http.Request) {
		// init opentelemetry tracer
		ctx := setTracer(r.Context(), s.tracer)
		otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(w.Header()))

		// create timeout context if options set
		if s.options.RequestTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, s.options.RequestTimeout)
			defer cancel()
		}

		res := handler(ctx, r)

		if err := s.sendResponse(ctx, w, res); err != nil {
			s.log(ctx, "web-respond", "ERROR", err)
			return
		}

		return
	}
}

func (s *Server) sendResponse(ctx context.Context, w http.ResponseWriter, res Encoder) error {
	// check if the context was cancelled
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			// log the error and no need to send a response
			s.log(ctx, "web-respond", "ERROR", err)
			return nil
		}
	}

	var statusCode int

	switch v := res.(type) {
	case httpStatus:
		statusCode = v.Status()
	case error:
		statusCode = http.StatusInternalServerError
	default:
		if res == nil {
			statusCode = http.StatusNoContent
		} else {
			// An encoder without an explicit status is a success payload.
			statusCode = http.StatusOK
		}
	}

	_, span := addSpan(ctx, "web.send.response", attribute.Int("status", statusCode))
	defer span.End()

	if statusCode == http.StatusNoContent {
		w.WriteHeader(statusCode)
		return nil
	}

	data, contentType, err := res.Encode()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("respond: encode: %w", err)
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(statusCode)

	if _, err := w.Write(data); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("respond: write: %w", err)
	}

	return nil
}
