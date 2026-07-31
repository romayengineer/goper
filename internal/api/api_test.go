package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/romayengineer/goper/internal/capture"
)

func TestCAURL(t *testing.T) {
	got := CAURL(9099)
	want := "http://localhost:9099/api/ca.pem"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNewServerReturnsServer(t *testing.T) {
	store := &mockStore{}
	server := NewServer(18081, store, nil)

	s, ok := server.(*Server)
	if !ok {
		t.Fatalf("expected *Server concrete type, got %T", server)
	}
	if s.handler == nil {
		t.Fatal("expected handler to be wired")
	}
	if s.port != 18081 {
		t.Fatalf("port: got %d", s.port)
	}
}

func TestServerRoutes(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("a")}}
	server := NewServer(0, store, []byte("pem"))
	s := server.(*Server)

	cases := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/api/requests", http.StatusOK},
		{http.MethodGet, "/api/requests/a", http.StatusOK},
		{http.MethodGet, "/api/requests/unknown", http.StatusNotFound},
		{http.MethodDelete, "/api/requests", http.StatusOK},
		{http.MethodGet, "/api/stats", http.StatusOK},
		{http.MethodGet, "/api/ca.pem", http.StatusOK},
		{http.MethodGet, "/nonexistent", http.StatusNotFound},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		s.router.ServeHTTP(rec, req)

		if rec.Code != tc.want {
			t.Fatalf("%s %s: got status %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

func TestServerCORS(t *testing.T) {
	server := NewServer(0, &mockStore{}, nil)
	s := server.(*Server)

	req := httptest.NewRequest(http.MethodOptions, "/api/requests", nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("OPTIONS status: got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("CORS origin: got %q", got)
	}
}

func TestServerImplementsRunnable(t *testing.T) {
	var _ Runnable = NewServer(0, &mockStore{}, nil)
}
