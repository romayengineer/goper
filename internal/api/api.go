package api

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/romayengineer/goper/internal/capture"
)

type Server struct {
	handler *Handler
	router  *chi.Mux
	port    int
}

func NewServer(port int, store capture.Store, caPEM []byte) *Server {
	handler := NewHandler(store, caPEM)

	r := chi.NewRouter()
	r.Use(slogMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	r.Get("/api/requests", handler.ListRequests)
	r.Get("/api/requests/stream", handler.StreamRequests)
	r.Get("/api/requests/{id}", handler.GetRequest)
	r.Delete("/api/requests", handler.ClearRequests)
	r.Get("/api/stats", handler.GetStats)
	r.Get("/api/ca.pem", handler.GetCA)

	return &Server{
		handler: handler,
		router:  r,
		port:    port,
	}
}

func (s *Server) Run() error {
	addr := fmt.Sprintf(":%d", s.port)
	slog.Info("api listening", "addr", addr)
	return http.ListenAndServe(addr, s.router)
}

func (s *Server) RunWithListener(l net.Listener) error {
	slog.Info("api listening", "addr", l.Addr())
	return http.Serve(l, s.router)
}

func CAURL(port int) string {
	return fmt.Sprintf("http://localhost:%d/api/ca.pem", port)
}

func slogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		slog.Info("api request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"duration", time.Since(start).String(),
		)
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
