package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/romayengineer/goper/internal/capture"
	"github.com/stretchr/testify/assert"
)

type flushRecorder struct {
	*httptest.ResponseRecorder
}

func (f *flushRecorder) Flush() {}

func TestStreamRequests(t *testing.T) {
	store := &mockStore{}
	h := newHandler(store, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/requests/stream", nil).WithContext(ctx)
	rec := &flushRecorder{httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.StreamRequests(rec, req)
	}()

	time.Sleep(50 * time.Millisecond)
	store.Push(sampleEntry("live"))
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream to close")
	}

	assert.Contains(t, rec.Body.String(), "data: ")
}

type nonFlusher struct {
	http.ResponseWriter
}

func TestStreamRequestsNotFlusher(t *testing.T) {
	h := newHandler(&mockStore{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests/stream", nil)
	rec := httptest.NewRecorder()
	h.StreamRequests(nonFlusher{rec}, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

type failingSSEWriter struct {
	*httptest.ResponseRecorder
}

func (failingSSEWriter) Flush() {}

func (failingSSEWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write failed")
}

// TestStreamRequestsStopsOnWriteError covers the SSE error path: when the
// client connection can no longer accept writes, the handler must stop
// instead of looping forever.
func TestStreamRequestsStopsOnWriteError(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("a")}}
	h := newHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests/stream", nil)
	rec := &failingSSEWriter{httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.StreamRequests(rec, req)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream handler did not stop when writes failed")
	}
}
