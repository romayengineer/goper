package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/romayengineer/goper/internal/capture"
	"github.com/stretchr/testify/assert"
)

func TestCAURL(t *testing.T) {
	assert.Equal(t, "http://localhost:9099/api/ca.pem", CAURL(9099))
}

func TestNewServerReturnsServer(t *testing.T) {
	store := &mockStore{}
	server := NewServer(18081, store, nil)

	s, ok := server.(*Server)
	if !assert.True(t, ok, "expected *Server concrete type, got %T", server) {
		return
	}
	assert.NotNil(t, s.handler, "expected handler to be wired")
	assert.Equal(t, 18081, s.port)
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

		assert.Equal(t, tc.want, rec.Code, "%s %s", tc.method, tc.path)
	}
}

func TestServerCORS(t *testing.T) {
	server := NewServer(0, &mockStore{}, nil)
	s := server.(*Server)

	req := httptest.NewRequest(http.MethodOptions, "/api/requests", nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestServerImplementsRunnable(t *testing.T) {
	var _ Runnable = NewServer(0, &mockStore{}, nil)
}
