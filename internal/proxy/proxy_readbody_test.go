package proxy

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elazarl/goproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadBodyNilBody(t *testing.T) {
	b, truncated, skipped, err := readBodyBounded(&http.Response{Body: nil}, 0)
	assert.NoError(t, err)
	assert.Empty(t, b)
	assert.False(t, truncated)
	assert.False(t, skipped)
}

func TestReadBodyEmptyBody(t *testing.T) {
	b, truncated, skipped, err := readBodyBounded(&http.Response{Body: io.NopCloser(strings.NewReader(""))}, 0)
	assert.NoError(t, err)
	assert.Empty(t, b)
	assert.False(t, truncated)
	assert.False(t, skipped)
}

// errOnRead returns an error if any Read reaches the underlying reader; the
// streaming-skip path must never touch the body.
type errOnRead struct{}

func (errOnRead) Read([]byte) (int, error) { return 0, errors.New("body must not be read") }

func TestReadBodyStreamingContentTypeSkipped(t *testing.T) {
	resp := &http.Response{
		ContentLength: -1,
		Header:        http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:          io.NopCloser(errOnRead{}),
	}
	got, truncated, skipped, err := readBodyBounded(resp, 1<<20)
	assert.NoError(t, err)
	assert.Nil(t, got)
	assert.False(t, truncated)
	assert.True(t, skipped, "streaming responses must be skipped, never buffered")
}

func TestReadBodyStreamingContentTypeSkippedUnlimited(t *testing.T) {
	resp := &http.Response{
		ContentLength: -1,
		Header:        http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}},
		Body:          io.NopCloser(errOnRead{}),
	}
	got, truncated, skipped, err := readBodyBounded(resp, 0)
	assert.NoError(t, err)
	assert.Nil(t, got)
	assert.False(t, truncated)
	assert.True(t, skipped)
}

func TestReadBodyChunkedBoundedCaptured(t *testing.T) {
	body := `{"chunked":true}`
	resp := &http.Response{
		ContentLength: -1,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
	}
	got, truncated, skipped, err := readBodyBounded(resp, 1<<20)
	assert.NoError(t, err)
	assert.Equal(t, body, string(got))
	assert.False(t, truncated)
	assert.False(t, skipped)

	rest, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, body, string(rest), "full body must be re-served after capture")
}

func TestReadBodyErrorRestoresPartialBody(t *testing.T) {
	prefix := `{"a":1`
	remainder := `,"b":2}`
	// A reader that yields `prefix`, fails once (mid-body network error), then
	// yields the `remainder` — so the untouched rest of the body is still
	// available after the failure.
	body := &failAfterReader{prefix: strings.NewReader(prefix), tail: strings.NewReader(remainder)}
	resp := &http.Response{ContentLength: -1, Body: io.NopCloser(body)}

	got, truncated, skipped, err := readBodyBounded(resp, 0)
	assert.Error(t, err)
	assert.False(t, truncated)
	assert.False(t, skipped)
	_ = got

	rest, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, prefix+remainder, string(rest), "client must receive prefix + remainder despite the read error")
}

func TestReadBodyKnownLengthUnlimited(t *testing.T) {
	body := strings.Repeat("x", 8<<20) // 8 MiB
	resp := &http.Response{
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(strings.NewReader(body)),
	}
	got, truncated, skipped, err := readBodyBounded(resp, 0)
	assert.NoError(t, err)
	assert.Equal(t, len(body), len(got))
	assert.False(t, truncated)
	assert.False(t, skipped)

	rest, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, body, string(rest))
}

func TestReadBodyUnknownLengthUnlimitedCapped(t *testing.T) {
	origCap := unlimitedReadSafetyCap
	unlimitedReadSafetyCap = 1024
	defer func() { unlimitedReadSafetyCap = origCap }()

	// Unknown length + unlimited: captured at most the safety cap, the full
	// body (cap + remainder) is still re-served to the client.
	tail := "tail"
	body := strings.Repeat("y", int(unlimitedReadSafetyCap)+len(tail))
	resp := &http.Response{
		ContentLength: -1,
		Body:          io.NopCloser(strings.NewReader(body)),
	}
	got, truncated, skipped, err := readBodyBounded(resp, 0)
	assert.NoError(t, err)
	assert.Equal(t, int(unlimitedReadSafetyCap), len(got))
	assert.True(t, truncated, "unknown-length body beyond the safety cap must be flagged truncated")
	assert.False(t, skipped)

	rest, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, body, string(rest), "full body must be re-served despite the capture cap")
}

// failAfterReader yields all of prefix, then fails once (mimicking a mid-body
// network error), then delegates to tail — so the untouched remainder of the
// body is still readable after the failure.
type failAfterReader struct {
	prefix *strings.Reader
	tail   io.Reader
	failed bool
}

func (r *failAfterReader) Read(p []byte) (int, error) {
	if r.prefix.Len() > 0 {
		return r.prefix.Read(p)
	}
	if !r.failed {
		r.failed = true
		return 0, errors.New("injected read failure")
	}
	return r.tail.Read(p)
}

func TestHandleResponseAppliesResponseBodyLimit(t *testing.T) {
	cfg := testConfig(t)
	cfg.responseBodyLimit = 5
	s := newTestServer(t, cfg)
	store := &mockStore{}
	s.store = store

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	reqCtx := &goproxy.ProxyCtx{}
	s.handleRequest(req, reqCtx)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"big":true}`)), // 12 bytes > 5
	}
	s.handleResponse(resp, &goproxy.ProxyCtx{UserData: reqCtx.UserData})

	require.Len(t, store.pushed, 1)
	assert.Nil(t, store.pushed[0].ResponseBody, "body over the limit must not be stored")
	assert.Equal(t, http.StatusOK, store.pushed[0].StatusCode, "the entry itself is still captured")
}

func TestHandleRequestAppliesRequestBodyLimit(t *testing.T) {
	cfg := testConfig(t)
	cfg.requestBodyLimit = 4
	s := newTestServer(t, cfg)

	req := httptest.NewRequest(http.MethodPost, "http://example.com/api", strings.NewReader("a long body"))
	ctx := &goproxy.ProxyCtx{}
	s.handleRequest(req, ctx)

	data, ok := ctx.UserData.(captureCtx)
	require.True(t, ok)
	assert.Nil(t, data.entry.RequestBody, "request body over the limit must not be stored")

	restored, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Equal(t, "a long body", string(restored), "body must still be restored for proxying")
}

func TestShouldCaptureExcludeWinsOverInclude(t *testing.T) {
	cfg := testConfig(t)
	cfg.captureInclude = `example\.com`
	cfg.captureExclude = `ads`
	s := newTestServer(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/ads/pixel", nil)
	assert.False(t, s.shouldCapture(req), "exclude must win over include on overlap")

	reqOK := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	assert.True(t, s.shouldCapture(reqOK))
}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingReadCloser) Close() error             { return nil }

// TestHandleResponseReadBodyErrorSkipsCapture covers the error path in the
// response pipeline: if the response body cannot be read, the response must
// still pass through to the client untouched, but nothing is captured.
func TestHandleResponseReadBodyErrorSkipsCapture(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)
	store := &mockStore{}
	s.store = store

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	reqCtx := &goproxy.ProxyCtx{}
	s.handleRequest(req, reqCtx)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       failingReadCloser{},
	}

	got := s.handleResponse(resp, &goproxy.ProxyCtx{UserData: reqCtx.UserData})
	assert.Same(t, resp, got, "response must pass through even when the body cannot be read")
	assert.Empty(t, store.pushed, "unreadable body must not be captured")
}
