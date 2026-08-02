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

func testConfig(t *testing.T) *config.Config {
	return &config.Config{
		Port:       8080,
		APIPort:    8081,
		CADir:      t.TempDir(),
		BufferSize: 100,
		LogLevel:   slog.LevelInfo,
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
	cfg.CaptureInclude = "[unclosed"
	_, err := NewServer(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "capture-include")

	cfg2 := testConfig(t)
	cfg2.CaptureExclude = "[unclosed"
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
