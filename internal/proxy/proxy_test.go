package proxy

import (
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
	port         int
	apiPort      int
	caDir        string
	transparent  bool
	verbose      bool
	bufferSize   int
	logFormat    string
	logLevel     slog.Level
	outputDir    string
	outputFormat string
}

func (m mockConfig) ProxyPort() int          { return m.port }
func (m mockConfig) GetAPIPort() int         { return m.apiPort }
func (m mockConfig) GetCADir() string        { return m.caDir }
func (m mockConfig) IsTransparent() bool     { return m.transparent }
func (m mockConfig) IsVerbose() bool         { return m.verbose }
func (m mockConfig) GetBufferSize() int      { return m.bufferSize }
func (m mockConfig) GetLogFormat() string    { return m.logFormat }
func (m mockConfig) GetLogLevel() slog.Level { return m.logLevel }
func (m mockConfig) GetOutputDir() string    { return m.outputDir }
func (m mockConfig) GetOutputFormat() string { return m.outputFormat }

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
func (m *mockStore) Clear()                   { m.pushed = nil }
func (m *mockStore) Len() int                 { return len(m.pushed) }
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
