package api

import (
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/romayengineer/goper/internal/capture"
)

type Server struct {
	handler *Handler
	router  *chi.Mux
	port    int
}

func NewServer(port int, store *capture.RingBuffer, caPEM []byte) *Server {
	handler := NewHandler(store, caPEM)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
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
	log.Printf("goper API listening on %s", addr)
	return http.ListenAndServe(addr, s.router)
}

func (s *Server) RunWithListener(l net.Listener) error {
	log.Printf("goper API listening on %s", l.Addr())
	return http.Serve(l, s.router)
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
