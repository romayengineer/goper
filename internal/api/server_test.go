package api

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/romayengineer/goper/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerRunWithListener serves the API over a real listener and verifies
// a live request round trip.
func TestServerRunWithListener(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("a")}}
	server := NewServer(0, store, []byte("pem"))
	s := server.(*Server)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	done := make(chan error, 1)
	go func() { done <- s.RunWithListener(ln) }()

	url := "http://" + ln.Addr().String() + "/api/stats"
	resp, err := http.Get(url) // #nosec G107 -- test URL from an httptest listener
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), `"count":1`)

	_ = ln.Close()
	select {
	case err := <-done:
		assert.Error(t, err, "Serve should return an error once the listener closes")
	case <-time.After(2 * time.Second):
		t.Fatal("RunWithListener did not return after listener close")
	}
}

// TestServerRunListenError verifies Run reports a bind failure instead of
// exiting silently. The blocker binds ALL interfaces (":0") so the second
// bind fails deterministically on both Linux and macOS/BSD (a 127.0.0.1-only
// blocker would be masked by SO_REUSEADDR semantics on BSD).
func TestServerRunListenError(t *testing.T) {
	blocker, err := net.Listen("tcp", ":0") // #nosec G102 -- deliberate all-interface bind for the conflict test
	require.NoError(t, err)
	defer func() { _ = blocker.Close() }()
	port := blocker.Addr().(*net.TCPAddr).Port

	store := &mockStore{}
	server := NewServer(port, store, nil)

	err = server.Run()
	require.Error(t, err, "binding an already-used port must fail")
}

func TestServerRunWithListenerServesUI(t *testing.T) {
	server := NewServer(0, &mockStore{}, nil)
	s := server.(*Server)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	done := make(chan error, 1)
	go func() { done <- s.RunWithListener(ln) }()
	defer func() { _ = ln.Close() }()

	req, err := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String()+"/ui", nil)
	require.NoError(t, err)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
