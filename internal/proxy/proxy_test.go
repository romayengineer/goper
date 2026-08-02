package proxy

import (
	"log/slog"
	"net/http"
	"testing"
	"time"

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
