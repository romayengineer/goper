package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/romayengineer/goper/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestHandlerImplementsRequestHandler(t *testing.T) {
	var _ RequestHandler = newHandler(&mockStore{}, nil)
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusCreated, map[string]string{"ok": "true"})

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}
