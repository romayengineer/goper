package api

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/romayengineer/goper/internal/capture"
)

// replayMaxBody caps how much of a replayed response is returned to the
// caller (the response itself is streamed in full to the upstream target).
const replayMaxBody = 1 << 20 // 1 MiB

// replayClient is shared across replay requests; a single client keeps the
// connection pool warm and avoids re-initializing TLS state per call.
var replayClient = &http.Client{Timeout: 30 * time.Second}

// hopByHopHeaders are stripped when rebuilding a replayed request; they are
// connection-specific and meaningless (or actively harmful) when resent.
var hopByHopHeaders = map[string]bool{
	"connection":        true,
	"proxy-connection":  true,
	"keep-alive":        true,
	"transfer-encoding": true,
	"te":                true,
	"trailer":           true,
	"upgrade":           true,
	"host":              true,
	"content-length":    true,
}

// copyReplayHeaders copies captured request headers onto a replay request,
// skipping hop-by-hop headers.
func copyReplayHeaders(req *http.Request, headers map[string]string) {
	for k, v := range headers {
		if hopByHopHeaders[strings.ToLower(k)] {
			continue
		}
		req.Header.Set(k, v)
	}
}

// buildReplayRequest reconstructs a captured request for replay.
func buildReplayRequest(entry *capture.CapturedEntry) (*http.Request, error) {
	var body io.Reader
	if entry.RequestBody != nil {
		body = strings.NewReader(*entry.RequestBody)
	}
	req, err := http.NewRequest(entry.Method, entry.URL, body) // #nosec G704 -- replay is an explicit feature: it resends a captured request to its stored URL
	if err != nil {
		return nil, err
	}
	copyReplayHeaders(req, entry.RequestHeaders)
	return req, nil
}

// ReplayRequest re-sends a captured request to its original URL using the
// stored method, headers and body, and returns the fresh response. Useful for
// re-executing an API call after a fix, or for testing idempotent endpoints.
func (h *Handler) ReplayRequest(w http.ResponseWriter, r *http.Request) {
	id := capture.EntryID(chi.URLParam(r, "id"))
	entry := h.store.Get(id)
	if entry == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "entry not found"})
		return
	}

	h.replayEntry(w, entry)
}

// replayEntry re-executes a captured request and writes the fresh response.
func (h *Handler) replayEntry(w http.ResponseWriter, entry *capture.CapturedEntry) {
	req, err := buildReplayRequest(entry)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	start := time.Now()
	resp, respBody, err := performReplay(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        string(respBody),
		"duration_ms": time.Since(start).Milliseconds(),
	})
}

// performReplay sends the replay request and reads up to replayMaxBody bytes of
// the response.
func performReplay(req *http.Request) (resp *http.Response, respBody []byte, err error) {
	resp, err = replayClient.Do(req) // #nosec G704 -- replay is an explicit feature: it resends a captured request to its stored URL
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err = io.ReadAll(io.LimitReader(resp.Body, replayMaxBody))
	if err != nil {
		return nil, nil, err
	}
	return resp, respBody, nil
}
