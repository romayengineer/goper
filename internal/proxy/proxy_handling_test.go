package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elazarl/goproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleRequest(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)
	rec := &mockRecorder{}
	s.recorder = rec

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	ctx := &goproxy.ProxyCtx{}

	returned, resp := s.handleRequest(req, ctx)
	assert.Same(t, req, returned)
	assert.Nil(t, resp)

	data, ok := ctx.UserData.(captureCtx)
	require.True(t, ok, "expected captureCtx in UserData, got %T", ctx.UserData)
	assert.Equal(t, http.MethodGet, data.entry.Method)
	assert.False(t, data.start.IsZero(), "expected start time set")
	assert.Equal(t, 1, rec.captureRequests)
}

func TestShouldCaptureDefaultsToTrue(t *testing.T) {
	s := newTestServer(t, testConfig(t))
	req := httptest.NewRequest(http.MethodGet, "http://example.com/anything?q=1", nil)
	assert.True(t, s.shouldCapture(req), "no filters configured means capture everything")
}

func TestShouldCaptureIncludeFilter(t *testing.T) {
	cfg := testConfig(t)
	cfg.captureInclude = `\.json$`
	s := newTestServer(t, cfg)

	reqJSON := httptest.NewRequest(http.MethodGet, "http://example.com/api/users.json", nil)
	assert.True(t, s.shouldCapture(reqJSON))

	reqHTML := httptest.NewRequest(http.MethodGet, "http://example.com/index.html", nil)
	assert.False(t, s.shouldCapture(reqHTML))
}

func TestShouldCaptureExcludeFilter(t *testing.T) {
	cfg := testConfig(t)
	cfg.captureExclude = `(google-analytics|doubleclick)`
	s := newTestServer(t, cfg)

	reqOK := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	assert.True(t, s.shouldCapture(reqOK))

	reqTracked := httptest.NewRequest(http.MethodGet, "http://example.com/doubleclick/pixel?x=1", nil)
	assert.False(t, s.shouldCapture(reqTracked))
}

func TestHandleRequestSkippedByFilter(t *testing.T) {
	cfg := testConfig(t)
	cfg.captureExclude = `^http://example\.com/static/`
	s := newTestServer(t, cfg)
	rec := &mockRecorder{}
	s.recorder = rec

	req := httptest.NewRequest(http.MethodGet, "http://example.com/static/app.js", nil)
	ctx := &goproxy.ProxyCtx{}

	returned, resp := s.handleRequest(req, ctx)
	assert.Same(t, req, returned)
	assert.Nil(t, resp)
	assert.Nil(t, ctx.UserData, "filtered request must not be captured")
	assert.Zero(t, rec.captureRequests)

	// And the response handler no-ops for it.
	store := &mockStore{}
	s.store = store
	respObj := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}
	got := s.handleResponse(respObj, &goproxy.ProxyCtx{UserData: nil})
	assert.Same(t, respObj, got)
	assert.Empty(t, store.pushed, "filtered traffic must not reach the store")
}

func TestHandleResponse(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)
	rec := &mockRecorder{}
	store := &mockStore{}
	s.recorder = rec
	s.store = store

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	reqCtx := &goproxy.ProxyCtx{}
	s.handleRequest(req, reqCtx)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}

	ctx := &goproxy.ProxyCtx{UserData: reqCtx.UserData}
	got := s.handleResponse(resp, ctx)
	assert.Same(t, resp, got)

	require.Len(t, store.pushed, 1)
	entry := store.pushed[0]
	assert.Equal(t, http.StatusOK, entry.StatusCode)
	assert.Equal(t, "application/json", entry.ContentType)
	require.NotNil(t, entry.ResponseBody)
	assert.Equal(t, `{"ok":true}`, *entry.ResponseBody)
	assert.Equal(t, 1, rec.captureResponses)
	assert.Equal(t, 1, rec.combined)
}

func TestHandleResponseWritesOutputs(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)
	s.store = &mockStore{}
	mo := &mockOutput{}
	s.AddOutput(mo)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	reqCtx := &goproxy.ProxyCtx{}
	s.handleRequest(req, reqCtx)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}

	s.handleResponse(resp, &goproxy.ProxyCtx{UserData: reqCtx.UserData})

	assert.Len(t, mo.entries, 1)
}

func TestHandleResponseOutputErrorDoesNotFail(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)
	store := &mockStore{}
	s.store = store
	s.AddOutput(&mockOutput{err: errOutput})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	reqCtx := &goproxy.ProxyCtx{}
	s.handleRequest(req, reqCtx)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}

	got := s.handleResponse(resp, &goproxy.ProxyCtx{UserData: reqCtx.UserData})
	assert.Same(t, resp, got, "output error should not break the response")
	assert.Len(t, store.pushed, 1, "entry should still be pushed despite output error")
}

func TestHandleResponseWithoutCaptureCtx(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("ok")),
	}
	got := s.handleResponse(resp, &goproxy.ProxyCtx{})
	assert.Same(t, resp, got, "expected response passed through untouched")
}

func TestHandleResponseNilResponse(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)

	got := s.handleResponse(nil, &goproxy.ProxyCtx{})
	assert.Nil(t, got, "expected nil response to pass through")
}

var errOutput = &outputError{}

type outputError struct{}

func (*outputError) Error() string { return "output error" }

func TestHandleResponseNoUserData(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)
	store := &mockStore{}
	s.store = store

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("x")),
	}
	got := s.handleResponse(resp, &goproxy.ProxyCtx{})
	assert.Same(t, resp, got, "response must pass through when capture was skipped")
	assert.Empty(t, store.pushed)
}
