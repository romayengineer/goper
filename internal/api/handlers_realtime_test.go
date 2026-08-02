package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/romayengineer/goper/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- tests below exercise the API against the real RingBuffer ----

func TestListRequestsPaginationThroughAPI(t *testing.T) {
	store := capture.NewRingBuffer(100)
	for i := 0; i < 5; i++ {
		store.Push(sampleEntry(string(rune('a' + i))))
	}
	h := NewHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests?limit=2&offset=2", nil)
	rec := httptest.NewRecorder()
	h.ListRequests(rec, req)

	count, data := decodeList(t, rec)
	assert.Equal(t, 2, count)
	require.Len(t, data, 2)
	assert.Equal(t, "c", string(data[0].ID))
	assert.Equal(t, "d", string(data[1].ID))
}

func TestListRequestsFiltersThroughAPI(t *testing.T) {
	store := capture.NewRingBuffer(100)
	post := sampleEntry("post")
	post.Method = "POST"
	post.StatusCode = 201
	post.URL = "http://example.com/items"
	store.Push(sampleEntry("get"))
	store.Push(post)
	h := NewHandler(store, nil)

	cases := []struct {
		query  string
		wantID string
	}{
		{"?method=POST", "post"},
		{"?status=201", "post"},
		{"?url=http://example.com/items", "post"},
		{"?method=GET", "get"},
		{"?method=DELETE", ""}, // no match
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/requests"+tc.query, nil)
		rec := httptest.NewRecorder()
		h.ListRequests(rec, req)
		count, data := decodeList(t, rec)
		if tc.wantID == "" {
			assert.Zero(t, count, "query %s", tc.query)
			continue
		}
		require.Equal(t, 1, count, "query %s", tc.query)
		assert.Equal(t, tc.wantID, string(data[0].ID), "query %s", tc.query)
	}
}

func TestListRequestsSinceThroughAPI(t *testing.T) {
	store := capture.NewRingBuffer(100)
	old := sampleEntry("old")
	old.Timestamp = time.Now().Add(-time.Hour)
	store.Push(old)
	store.Push(sampleEntry("new"))
	h := NewHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests?since="+time.Now().Add(-time.Minute).Format(time.RFC3339), nil)
	rec := httptest.NewRecorder()
	h.ListRequests(rec, req)

	count, data := decodeList(t, rec)
	assert.Equal(t, 1, count)
	require.Len(t, data, 1)
	assert.Equal(t, "new", string(data[0].ID))
}

func TestGetStatsThroughRealStore(t *testing.T) {
	store := capture.NewRingBuffer(10)
	body := `{"x":1}` // 7 bytes
	e := sampleEntry("a")
	e.RequestBody = &body
	e.ResponseBody = &body
	store.Push(e)
	h := NewHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rec := httptest.NewRecorder()
	h.GetStats(rec, req)

	var s struct {
		Count         int   `json:"count"`
		Capacity      int   `json:"capacity"`
		BytesCaptured int64 `json:"bytes_captured"`
		UptimeSeconds int64 `json:"uptime_seconds"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &s))
	assert.Equal(t, 1, s.Count)
	assert.Equal(t, 10, s.Capacity)
	assert.Equal(t, int64(14), s.BytesCaptured, "request + response bodies")
	assert.GreaterOrEqual(t, s.UptimeSeconds, int64(0))
}

func TestStreamRequestsBackfillCountsWithRealStore(t *testing.T) {
	store := capture.NewRingBuffer(100)
	for i := 0; i < 3; i++ {
		store.Push(sampleEntry(string(rune('a' + i))))
	}
	h := NewHandler(store, nil)

	cases := []struct {
		query    string
		wantData int
	}{
		{"?backfill=2", 2},
		{"?backfill=0", 0},
		{"?backfill=abc", 3}, // invalid → default 50, only 3 exist
		{"?backfill=-1", 3},  // negative → default 50, only 3 exist
		{"", 3},              // default backfill
	}
	for _, tc := range cases {
		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodGet, "/api/requests/stream"+tc.query, nil).WithContext(ctx)
		rec := &flushRecorder{httptest.NewRecorder()}
		done := make(chan struct{})
		go func() {
			defer close(done)
			h.StreamRequests(rec, req)
		}()
		time.Sleep(50 * time.Millisecond)
		cancel()
		<-done

		got := strings.Count(rec.Body.String(), "data: ")
		assert.Equal(t, tc.wantData, got, "query %s", tc.query)
	}
}

func TestReplayRequestGETEntry(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("fresh"))
	}))
	defer target.Close()

	entry := sampleEntry("abc")
	entry.Method = "GET"
	entry.URL = target.URL
	entry.RequestHeaders = map[string]string{"Accept": "text/plain"}
	// RequestBody is nil — replay must still work for bodyless requests.

	h := NewHandler(&mockStore{entries: []*capture.CapturedEntry{entry}}, nil)

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
	assert.Equal(t, "fresh", resp.Body)
}
