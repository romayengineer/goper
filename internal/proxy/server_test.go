package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"testing"
	"time"

	"github.com/romayengineer/goper/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helpers + live round-trip tests. These are intentionally NOT behind the
// integration build tag so plain `go test` and `make cover` exercise the real
// serving path (RunWithListener → goproxy → capture pipeline) over loopback.

func startProxy(t *testing.T) (*Server, string) {
	t.Helper()
	s := newTestServer(t, testConfig(t))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- s.RunWithListener(ln) }()
	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})
	return s, ln.Addr().String()
}

func proxyClient(t *testing.T, proxyURL string, rootCAs *x509.CertPool) *http.Client {
	t.Helper()
	transport := &http.Transport{
		Proxy: http.ProxyURL(mustURL(t, proxyURL)),
	}
	if rootCAs != nil {
		transport.TLSClientConfig = &tls.Config{RootCAs: rootCAs}
	}
	return &http.Client{Transport: transport}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

func waitForEntry(t *testing.T, store capture.Store) *capture.CapturedEntry {
	t.Helper()
	var entry *capture.CapturedEntry
	assert.Eventually(t, func() bool {
		entries := store.List(capture.ListOpts{})
		if len(entries) > 0 {
			entry = entries[0]
			return true
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)
	return entry
}

func TestProxyRunWithListenerHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer upstream.Close()

	s, proxyAddr := startProxy(t)
	client := proxyClient(t, "http://"+proxyAddr, nil)

	resp, err := client.Get(upstream.URL + "/api/data")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"hello":"world"}`, string(body))

	entry := waitForEntry(t, s.Store())
	require.NotNil(t, entry)
	assert.Equal(t, http.MethodGet, entry.Method)
	assert.Equal(t, http.StatusOK, entry.StatusCode)
	assert.Contains(t, entry.URL, "/api/data")
	require.NotNil(t, entry.ResponseBody)
	assert.JSONEq(t, `{"hello":"world"}`, *entry.ResponseBody)
	assert.Equal(t, "application/json", entry.ContentType)
}

func TestProxyRunWithListenerMITM(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"secure":true}`))
	}))
	defer upstream.Close()

	s, proxyAddr := startProxy(t)

	upstreamPool := x509.NewCertPool()
	upstreamPool.AddCert(upstream.Certificate())
	s.proxy.Tr = &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: upstreamPool},
	}

	clientPool := x509.NewCertPool()
	clientPool.AddCert(s.CA().Certificate())
	client := proxyClient(t, "http://"+proxyAddr, clientPool)

	resp, err := client.Get(upstream.URL + "/secure")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"secure":true}`, string(body))

	entry := waitForEntry(t, s.Store())
	require.NotNil(t, entry)
	assert.Equal(t, http.MethodGet, entry.Method)
	assert.Equal(t, http.StatusOK, entry.StatusCode)
	assert.Equal(t, "https", entry.Scheme)
	require.NotNil(t, entry.ResponseBody)
	assert.JSONEq(t, `{"secure":true}`, *entry.ResponseBody)
}

// TestServerRunListenError verifies Run reports a bind failure instead of
// hanging or exiting silently. The blocker binds ALL interfaces (":0") so the
// second bind fails deterministically on both Linux and macOS/BSD.
func TestServerRunListenError(t *testing.T) {
	blocker, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer blocker.Close()
	port := blocker.Addr().(*net.TCPAddr).Port

	cfg := testConfig(t)
	cfg.port = port
	s := newTestServer(t, cfg)

	err = s.Run()
	require.Error(t, err, "binding an already-used port must fail")
}

// TestRunTransparentUnsupportedOnNonLinux covers the fail-fast path for
// transparent mode on platforms without an original-destination resolver.
func TestRunTransparentUnsupportedOnNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux has a real SO_ORIGINAL_DST resolver; this path is non-linux only")
	}

	cfg := testConfig(t)
	cfg.transparent = true
	s := newTestServer(t, cfg)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	err = s.RunWithListener(ln)
	require.Error(t, err)
	assert.Equal(t, errTransparentUnsupported, err)
}

// TestDefaultResolverOnNonLinux covers the platform default for the
// original-destination resolver.
func TestDefaultResolverOnNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux uses the SO_ORIGINAL_DST resolver")
	}
	assert.Nil(t, defaultResolver(), "non-linux platforms have no original-destination resolver")
}

// TestProxyRunWithListenerRejectsBadURL covers the goproxy path when the
// client sends a request the proxy cannot parse to an absolute URL (the
// request is still answered, never panics).
func TestProxyRunWithListenerRejectsBadURL(t *testing.T) {
	s, proxyAddr := startProxy(t)

	conn, err := net.Dial("tcp", proxyAddr)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.Write([]byte("GET http://127.0.0.1:1/%zz HTTP/1.1\r\nHost: x\r\n\r\n"))
	require.NoError(t, err)

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = io.ReadAll(conn)
	require.NoError(t, err)

	assert.Zero(t, s.Store().Len(), "malformed request must not be captured")
}
