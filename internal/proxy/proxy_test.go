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
)

type mockConfig struct {
	port        int
	apiPort     int
	caDir       string
	transparent bool
	verbose     bool
	bufferSize  int
	logFormat   string
	logLevel    slog.Level
}

func (m mockConfig) ProxyPort() int            { return m.port }
func (m mockConfig) GetAPIPort() int           { return m.apiPort }
func (m mockConfig) GetCADir() string          { return m.caDir }
func (m mockConfig) IsTransparent() bool       { return m.transparent }
func (m mockConfig) IsVerbose() bool           { return m.verbose }
func (m mockConfig) GetBufferSize() int        { return m.bufferSize }
func (m mockConfig) GetLogFormat() string      { return m.logFormat }
func (m mockConfig) GetLogLevel() slog.Level   { return m.logLevel }

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
func (m *mockStore) Clear()                     { m.pushed = nil }
func (m *mockStore) Len() int                   { return len(m.pushed) }
func (m *mockStore) Subscribe() chan *capture.CapturedEntry {
	return make(chan *capture.CapturedEntry)
}
func (m *mockStore) Unsubscribe(ch chan *capture.CapturedEntry) {}

type mockRecorder struct {
	captureRequests int
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
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s.(*Server)
}

func TestNewServer(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)

	if s.Store() == nil {
		t.Fatal("expected Store() to return non-nil")
	}
	if s.CA() == nil {
		t.Fatal("expected CA() to return non-nil")
	}
	if s.config != cfg {
		t.Fatal("expected config to be stored")
	}
	if s.recorder == nil {
		t.Fatal("expected default recorder")
	}
}

func TestNewServerImplementsRunnable(t *testing.T) {
	cfg := testConfig(t)
	var r Runnable
	var err error
	r, err = NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil Runnable")
	}
}

func TestAddOutput(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)

	mo := &mockOutput{}
	s.AddOutput(mo)

	if len(s.outputs) != 1 {
		t.Fatalf("expected 1 output, got %d", len(s.outputs))
	}
}

func TestHandleRequest(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)
	rec := &mockRecorder{}
	s.recorder = rec

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)
	ctx := &goproxy.ProxyCtx{}

	returned, resp := s.handleRequest(req, ctx)
	if returned != req {
		t.Fatal("expected same request returned")
	}
	if resp != nil {
		t.Fatal("expected nil response")
	}

	data, ok := ctx.UserData.(captureCtx)
	if !ok {
		t.Fatalf("expected captureCtx in UserData, got %T", ctx.UserData)
	}
	if data.entry.Method != http.MethodGet {
		t.Fatalf("entry method: got %q", data.entry.Method)
	}
	if data.start.IsZero() {
		t.Fatal("expected start time set")
	}
	if rec.captureRequests != 1 {
		t.Fatalf("expected 1 CaptureRequest call, got %d", rec.captureRequests)
	}
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
	if got != resp {
		t.Fatal("expected original response returned")
	}

	if len(store.pushed) != 1 {
		t.Fatalf("expected 1 entry pushed to store, got %d", len(store.pushed))
	}
	entry := store.pushed[0]
	if entry.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d", entry.StatusCode)
	}
	if entry.ContentType != "application/json" {
		t.Fatalf("content type: got %q", entry.ContentType)
	}
	if entry.ResponseBody == nil || *entry.ResponseBody != `{"ok":true}` {
		t.Fatalf("response body: got %v", entry.ResponseBody)
	}
	if rec.captureResponses != 1 || rec.combined != 1 {
		t.Fatalf("recorder calls: responses=%d combined=%d", rec.captureResponses, rec.combined)
	}
}

func TestHandleResponseWritesOutputs(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)
	store := &mockStore{}
	mo := &mockOutput{}
	s.store = store
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

	if len(mo.entries) != 1 {
		t.Fatalf("expected 1 output entry, got %d", len(mo.entries))
	}
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
	if got != resp {
		t.Fatal("output error should not break the response")
	}
	if len(store.pushed) != 1 {
		t.Fatalf("entry should still be pushed despite output error, got %d", len(store.pushed))
	}
}

func TestHandleResponseWithoutCaptureCtx(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("ok")),
	}
	got := s.handleResponse(resp, &goproxy.ProxyCtx{})
	if got != resp {
		t.Fatal("expected response passed through untouched")
	}
}

func TestHandleResponseNilResponse(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)

	got := s.handleResponse(nil, &goproxy.ProxyCtx{})
	if got != nil {
		t.Fatal("expected nil response to pass through")
	}
}

func TestServerImplementsRunnable(t *testing.T) {
	cfg := testConfig(t)
	s := newTestServer(t, cfg)
	var _ Runnable = s
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
