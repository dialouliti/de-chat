// Package server runs the HTTP server with graceful shutdown.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// Server wraps http.Server with managed lifecycle.
type Server struct {
	httpSrv *http.Server
	log     *slog.Logger
}

// New creates a Server bound to addr serving the given handler.
func New(addr string, handler http.Handler, log *slog.Logger) *Server {
	return &Server{
		httpSrv: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       120 * time.Second,
		},
		log: log,
	}
}

// Start blocks until the server stops. It returns the error from ListenAndServe
// unless it is http.ErrServerClosed (the expected result of Shutdown).
func (s *Server) Start() error {
	s.log.Info("http server starting", "addr", s.httpSrv.Addr)
	err := s.httpSrv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown gracefully drains in-flight requests, waiting up to 15s.
func (s *Server) Shutdown(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	s.log.Info("http server shutting down")
	return s.httpSrv.Shutdown(shutdownCtx)
}
