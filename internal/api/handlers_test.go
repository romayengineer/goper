package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/romayengineer/goper/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockStore struct {
	mu          sync.Mutex
	entries     []*capture.CapturedEntry
	subscribers []chan *capture.CapturedEntry
	cleared     bool
}

func (m *mockStore) Push(entry *capture.CapturedEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, entry)
	for _, ch := range m.subscribers {
		select {
		case ch <- entry:
		default:
		}
	}
}

func (m *mockStore) Get(id capture.EntryID) *capture.CapturedEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.entries {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func (m *mockStore) List(opts capture.ListOpts) []*capture.CapturedEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*capture.CapturedEntry
	for _, e := range m.entries {
		if !opts.Since.IsZero() && e.Timestamp.Before(opts.Since) {
			continue
		}
		if opts.Method != "" && e.Method != opts.Method {
			continue
		}
		if opts.Status > 0 && e.StatusCode != opts.Status {
			continue
		}
		if opts.URL != "" && e.URL != opts.URL {
			continue
		}
		out = append(out, e)
	}
	return out
}

func (m *mockStore) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleared = true
	m.entries = nil
}

func (m *mockStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

func (m *mockStore) Stats() capture.StoreStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return capture.StoreStats{
		Count:     len(m.entries),
		Capacity:  10000,
		StartTime: time.Now(),
	}
}

func (m *mockStore) Subscribe() chan *capture.CapturedEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan *capture.CapturedEntry, 10)
	m.subscribers = append(m.subscribers, ch)
	return ch
}

func (m *mockStore) Unsubscribe(ch chan *capture.CapturedEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.subscribers {
		if s == ch {
			m.subscribers = append(m.subscribers[:i], m.subscribers[i+1:]...)
			close(ch)
			return
		}
	}
}

func newHandler(store *mockStore, caPEM []byte) *Handler {
	return NewHandler(store, caPEM)
}

func sampleEntry(id string) *capture.CapturedEntry {
	return &capture.CapturedEntry{
		ID:         capture.EntryID(id),
		Timestamp:  time.Now(),
		Method:     "GET",
		URL:        "http://example.com/" + id,
		StatusCode: 200,
	}
}

func decodeList(t *testing.T, resp *httptest.ResponseRecorder) (int, []*capture.CapturedEntry) {
	t.Helper()
	var body struct {
		Count int                      `json:"count"`
		Data  []*capture.CapturedEntry `json:"data"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	return body.Count, body.Data
}

func TestListRequests(t *testing.T) {
	store := &mockStore{
		entries: []*capture.CapturedEntry{sampleEntry("a"), sampleEntry("b")},
	}
	h := newHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests", nil)
	rec := httptest.NewRecorder()
	h.ListRequests(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	count, data := decodeList(t, rec)
	assert.Equal(t, 2, count)
	assert.Len(t, data, 2)
}

func TestListRequestsEmpty(t *testing.T) {
	store := &mockStore{}
	h := newHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests", nil)
	rec := httptest.NewRecorder()
	h.ListRequests(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	count, data := decodeList(t, rec)
	assert.Equal(t, 0, count)
	assert.Empty(t, data)
}

func TestListRequestsFilters(t *testing.T) {
	post := sampleEntry("post")
	post.Method = "POST"
	store := &mockStore{
		entries: []*capture.CapturedEntry{sampleEntry("get"), post},
	}
	h := newHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests?method=POST", nil)
	rec := httptest.NewRecorder()
	h.ListRequests(rec, req)

	count, data := decodeList(t, rec)
	assert.Equal(t, 1, count)
	require.Len(t, data, 1)
	assert.Equal(t, "post", string(data[0].ID))
}

func TestListRequestsInvalidParamsIgnored(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("a")}}
	h := newHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests?since=not-a-date&status=abc&limit=-5&offset=-1", nil)
	rec := httptest.NewRecorder()
	h.ListRequests(rec, req)

	count, _ := decodeList(t, rec)
	assert.Equal(t, 1, count, "invalid params should be ignored")
}

func TestGetRequestFound(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("abc")}}
	h := newHandler(store, nil)

	r := chi.NewRouter()
	r.Get("/api/requests/{id}", h.GetRequest)

	req := httptest.NewRequest(http.MethodGet, "/api/requests/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var entry capture.CapturedEntry
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &entry))
	assert.Equal(t, "abc", string(entry.ID))
}

func TestGetRequestNotFound(t *testing.T) {
	store := &mockStore{}
	h := newHandler(store, nil)

	r := chi.NewRouter()
	r.Get("/api/requests/{id}", h.GetRequest)

	req := httptest.NewRequest(http.MethodGet, "/api/requests/missing", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestClearRequests(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("a")}}
	h := newHandler(store, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/requests", nil)
	rec := httptest.NewRecorder()
	h.ClearRequests(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, store.cleared, "expected store.Clear to be called")
}

func TestGetStats(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("a"), sampleEntry("b")}}
	h := newHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rec := httptest.NewRecorder()
	h.GetStats(rec, req)

	var body struct {
		Count          int   `json:"count"`
		Capacity       int   `json:"capacity"`
		Evictions      int64 `json:"evictions"`
		BytesCaptured  int64 `json:"bytes_captured"`
		UptimeSeconds  int64 `json:"uptime_seconds"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 2, body.Count)
	assert.Equal(t, 10000, body.Capacity)
	assert.Zero(t, body.Evictions)
	assert.Zero(t, body.BytesCaptured)
	assert.GreaterOrEqual(t, body.UptimeSeconds, int64(0))
}

func TestStreamRequestsBackfill(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("old1"), sampleEntry("old2")}}
	h := newHandler(store, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/requests/stream?backfill=2", nil).WithContext(ctx)
	rec := &flushRecorder{httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.StreamRequests(rec, req)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	assert.Contains(t, body, `"id":"old1"`, "expected historical entry in backfill")
	assert.Contains(t, body, `"id":"old2"`, "expected historical entry in backfill")
}

func TestStreamRequestsBackfillDisabled(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("old1")}}
	h := newHandler(store, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/requests/stream?backfill=0", nil).WithContext(ctx)
	rec := &flushRecorder{httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.StreamRequests(rec, req)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	assert.NotContains(t, rec.Body.String(), "old1", "backfill=0 must not send history")
}

func TestReplayRequest(t *testing.T) {
	var gotHost, gotMethod, gotBody string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"replayed":true}`))
	}))
	defer target.Close()

	body := `{"x":1}`
	entry := sampleEntry("abc")
	entry.Method = "POST"
	entry.URL = target.URL
	entry.RequestBody = &body
	entry.RequestHeaders = map[string]string{
		"Content-Type": "application/json",
		"X-Trace":      "abc",
		"Host":         "stale.example.com", // hop-by-hop: must be stripped
	}

	h := newHandler(&mockStore{entries: []*capture.CapturedEntry{entry}}, nil)

	r := chi.NewRouter()
	r.Post("/api/requests/{id}/replay", h.ReplayRequest)

	req := httptest.NewRequest(http.MethodPost, "/api/requests/abc/replay", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		StatusCode int    `json:"status_code"`
		Body       string `json:"body"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, `{"replayed":true}`, resp.Body)

	assert.Equal(t, "POST", gotMethod)
	assert.Equal(t, `{"x":1}`, gotBody)
	assert.NotEqual(t, "stale.example.com", gotHost, "stale Host header must be stripped on replay")
}

func TestReplayRequestNotFound(t *testing.T) {
	h := newHandler(&mockStore{}, nil)

	r := chi.NewRouter()
	r.Post("/api/requests/{id}/replay", h.ReplayRequest)

	req := httptest.NewRequest(http.MethodPost, "/api/requests/nope/replay", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetCA(t *testing.T) {
	pem := []byte("-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----")
	h := newHandler(&mockStore{}, pem)

	req := httptest.NewRequest(http.MethodGet, "/api/ca.pem", nil)
	rec := httptest.NewRecorder()
	h.GetCA(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, string(pem), rec.Body.String())
	assert.Equal(t, "application/x-pem-file", rec.Header().Get("Content-Type"))
}

func TestGetCANotFound(t *testing.T) {
	h := newHandler(&mockStore{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/ca.pem", nil)
	rec := httptest.NewRecorder()
	h.GetCA(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

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

func TestHandlerImplementsRequestHandler(t *testing.T) {
	var _ RequestHandler = newHandler(&mockStore{}, nil)
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusCreated, map[string]string{"ok": "true"})

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}
