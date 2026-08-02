package proxy

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/romayengineer/goper/internal/capture"
	"github.com/romayengineer/goper/internal/config"
	"github.com/romayengineer/goper/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockConfig struct {
	port              int
	apiPort           int
	caDir             string
	transparent       bool
	verbose           bool
	bufferSize        int
	logFormat         string
	logLevel          slog.Level
	outputDir         string
	outputFormat      string
	requestBodyLimit  int64
	responseBodyLimit int64
	captureInclude    string
	captureExclude    string
}

func (m mockConfig) ProxyPort() int              { return m.port }
func (m mockConfig) GetAPIPort() int             { return m.apiPort }
func (m mockConfig) GetCADir() string            { return m.caDir }
func (m mockConfig) IsTransparent() bool         { return m.transparent }
func (m mockConfig) IsVerbose() bool             { return m.verbose }
func (m mockConfig) GetBufferSize() int          { return m.bufferSize }
func (m mockConfig) GetLogFormat() string        { return m.logFormat }
func (m mockConfig) GetLogLevel() slog.Level     { return m.logLevel }
func (m mockConfig) GetOutputDir() string        { return m.outputDir }
func (m mockConfig) GetOutputFormat() string     { return m.outputFormat }
func (m mockConfig) GetRequestBodyLimit() int64  { return m.requestBodyLimit }
func (m mockConfig) GetResponseBodyLimit() int64 { return m.responseBodyLimit }
func (m mockConfig) GetCaptureInclude() string   { return m.captureInclude }
func (m mockConfig) GetCaptureExclude() string   { return m.captureExclude }

type mockStore struct {
	pushed []*capture.CapturedEntry
}

func (m *mockStore) Push(entry *capture.CapturedEntry) {
	m.pushed = append(m.pushed, entry)
}
func (m *mockStore) Get(id capture.EntryID) *capture.CapturedEntry {
	for _, e := range m.pushed {
		if e.ID == id {
			return e
		}
	}
	return nil
}
func (m *mockStore) List(opts capture.ListOpts) []*capture.CapturedEntry {
	return m.pushed
}
func (m *mockStore) Clear()   { m.pushed = nil }
func (m *mockStore) Len() int { return len(m.pushed) }
func (m *mockStore) Stats() capture.StoreStats {
	return capture.StoreStats{Count: len(m.pushed), Capacity: 100}
}
func (m *mockStore) Subscribe() chan *capture.CapturedEntry {
	return make(chan *capture.CapturedEntry)
}
func (m *mockStore) Unsubscribe(ch chan *capture.CapturedEntry) {}

type mockRecorder struct {
	captureRequests  int
	captureResponses int
	combined         int
}

func (m *mockRecorder) CaptureRequest(r *http.Request) capture.CapturedEntry {
	m.captureRequests++
	return capture.CaptureRequest(r)
}

func (m *mockRecorder) CaptureResponse(statusCode int, header http.Header, bodyBytes []byte, start time.Time) capture.CaptureResult {
	m.captureResponses++
	return capture.CaptureResponse(statusCode, header, bodyBytes, start)
}

func (m *mockRecorder) CombineEntry(reqEntry capture.CapturedEntry, result capture.CaptureResult) *capture.CapturedEntry {
	m.combined++
	return capture.CombineEntry(reqEntry, result)
}

type mockOutput struct {
	entries []*capture.CapturedEntry
	err     error
}

func (m *mockOutput) WriteEntry(entry *capture.CapturedEntry) error {
	if m.err != nil {
		return m.err
	}
	m.entries = append(m.entries, entry)
	return nil
}

func testConfig(t *testing.T) mockConfig {
	return mockConfig{
		port:       8080,
		apiPort:    8081,
		caDir:      t.TempDir(),
		bufferSize: 100,
		logLevel:   slog.LevelInfo,
	}
}

func newTestServer(t *testing.T, cfg config.Provider) *Server {
	t.Helper()
	s, err := NewServer(cfg)
	require.NoError(t, err)
	return s.(*Server)
}

func TestNewServer(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)

	assert.NotNil(t, s.Store())
	assert.NotNil(t, s.CA())
	assert.Equal(t, cfg, s.config)
	assert.NotNil(t, s.recorder, "expected default recorder")
}

func TestNewServerImplementsRunnable(t *testing.T) {
	cfg := testConfig(t)
	r, err := NewServer(cfg)
	require.NoError(t, err)
	assert.NotNil(t, r)
}

func TestAddOutput(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)

	s.AddOutput(&mockOutput{})

	assert.Len(t, s.outputs, 1)
}

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

func TestNewServerInvalidRegexes(t *testing.T) {
	cfg := testConfig(t)
	cfg.captureInclude = "[unclosed"
	_, err := NewServer(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "capture-include")

	cfg2 := testConfig(t)
	cfg2.captureExclude = "[unclosed"
	_, err = NewServer(cfg2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "capture-exclude")
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

func TestServerImplementsRunnable(t *testing.T) {
	cfg := testConfig(t)
	var _ Runnable = newTestServer(t, cfg)
}

func TestServerImplementsOutputWriter(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)
	mo := &mockOutput{}
	s.AddOutput(mo)
	var _ output.Writer = mo
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
