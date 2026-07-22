package http

import (
	"expvar"
	"log"
	"net/http"
	"net/http/pprof"

	"github.com/arl/statsviz"
)

// NewDebugServer creates a new debug server that exposes various debugging endpoints and variables.
func NewDebugServer(log *log.Logger, port string) (http.Server, error) {
	mux := http.NewServeMux()

	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/debug/vars/", expvar.Handler())

	if err := statsviz.Register(mux); err != nil {
		return http.Server{}, err
	}

	return http.Server{
		Addr:     port,
		Handler:  mux,
		ErrorLog: log,
	}, nil
}
