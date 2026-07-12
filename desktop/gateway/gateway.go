// Package gateway provides a lightweight HTTP server wrapper for the desktop API.
// It handles server lifecycle (start/stop), route registration, and a middleware
// chain skeleton. The package is deliberately minimal — no routing library, no
// framework — just the stdlib net/http with a thin convenience layer.
package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// HandlerFunc is the signature every API handler must satisfy.
type HandlerFunc func(w http.ResponseWriter, r *http.Request)

// Gateway wraps an http.Server with structured route registration.
// Middleware (auth, CORS, rate-limit) slots are reserved but not wired.
type Gateway struct {
	port int
	mux  *http.ServeMux
	srv  *http.Server
}

// New creates a Gateway that will listen on the given port.
// Call Start() to begin serving.
func New(port int) *Gateway {
	mux := http.NewServeMux()
	return &Gateway{
		port: port,
		mux:  mux,
		srv: &http.Server{
			Addr:         net.JoinHostPort("0.0.0.0", fmtPort(port)),
			Handler:      middlewareChain(mux),
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 0, // streaming (SSE) needs no write timeout
			IdleTimeout:  120 * time.Second,
		},
	}
}

// Handle registers a handler for the given method + path pattern.
// pattern should be the path only (e.g. "/api/workspaces").
func (g *Gateway) Handle(method, pattern string, handler HandlerFunc) {
	g.mux.HandleFunc(method+" "+pattern, handler)
}

// Start begins serving HTTP in a background goroutine.
// Returns an error only if the listener cannot be opened.
func (g *Gateway) Start() error {
	ln, err := net.Listen("tcp", g.srv.Addr)
	if err != nil {
		return err
	}
	slog.Info("desktop API server started", "addr", g.srv.Addr)
	go g.srv.Serve(ln) //nolint: errcheck
	return nil
}

// Stop performs a graceful shutdown with a 5-second timeout.
func (g *Gateway) Stop(ctx context.Context) error {
	slog.Info("desktop API server shutting down")
	return g.srv.Shutdown(ctx)
}

// Port returns the configured port.
func (g *Gateway) Port() int { return g.port }

// Handler returns the http.Handler with middleware applied.
// Useful for testing with httptest.
func (g *Gateway) Handler() http.Handler { return g.srv.Handler }

// --- helpers ---

func fmtPort(port int) string {
	if port <= 0 {
		return "7777"
	}
	return itoa(port)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [12]byte
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// middlewareChain is the reserved slot for future middleware (auth, CORS, etc).
// Currently it is a pass-through.
func middlewareChain(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

// WriteJSON is a convenience helper for handlers.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("gateway: failed to encode JSON", "err", err)
	}
}

// WriteError writes a JSON error response.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// DecodeBody reads and decodes a JSON request body into dst.
func DecodeBody(r *http.Request, dst any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dst)
}
