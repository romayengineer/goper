//go:build integration

package proxy

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/romayengineer/goper/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeResolver returns a canned original destination, standing in for the
// Linux SO_ORIGINAL_DST syscall.
type fakeResolver struct {
	ip   string
	port int
	err  error
}

func (f fakeResolver) Resolve(conn net.Conn) (*OriginalDst, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &OriginalDst{IP: net.ParseIP(f.ip), Port: f.port}, nil
}

func newTransparentServer(t *testing.T, upstreamPort int) (*Server, string) {
	t.Helper()
	cfg := testConfig(t)
	cfg.transparent = true

	s := newTestServer(t, cfg)
	s.resolver = fakeResolver{ip: "127.0.0.1", port: upstreamPort}
	s.peeker = DefaultSNIPeeker{}

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

func rawHTTPGet(t *testing.T, addr, host, path string, tlsCfg *tls.Config) *http.Response {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close()

	if tlsCfg != nil {
		tconn := tls.Client(conn, tlsCfg)
		require.NoError(t, tconn.Handshake())
		conn = tconn
	}

	_, err = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", path, host)
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	return resp
}

func lastEntry(t *testing.T, store capture.Store) *capture.CapturedEntry {
	t.Helper()
	var entry *capture.CapturedEntry
	assert.Eventually(t, func() bool {
		entries := store.List(capture.ListOpts{})
		if len(entries) > 0 {
			entry = entries[len(entries)-1]
			return true
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)
	return entry
}

func TestTransparentHTTPForward(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"transparent":true}`))
	}))
	defer upstream.Close()

	u, err := url.Parse(upstream.URL)
	require.NoError(t, err)

	s, proxyAddr := newTransparentServer(t, mustPort(t, u))

	resp := rawHTTPGet(t, proxyAddr, u.Host, "/data", nil)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"transparent":true}`, string(body))

	entry := lastEntry(t, s.Store())
	require.NotNil(t, entry)
	assert.Equal(t, "http", entry.Scheme)
	assert.Equal(t, "/data", entry.Path)
	require.NotNil(t, entry.ResponseBody)
	assert.JSONEq(t, `{"transparent":true}`, *entry.ResponseBody)
}

func TestTransparentHTTPSMITM(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"secure":true}`))
	}))
	defer upstream.Close()

	u, err := url.Parse(upstream.URL)
	require.NoError(t, err)

	s, proxyAddr := newTransparentServer(t, mustPort(t, u))

	upstreamPool := x509.NewCertPool()
	upstreamPool.AddCert(upstream.Certificate())
	s.proxy.Tr = &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: upstreamPool},
	}

	clientPool := x509.NewCertPool()
	clientPool.AddCert(s.CA().Certificate())

	resp := rawHTTPGet(t, proxyAddr, u.Host, "/secure", &tls.Config{
		RootCAs:    clientPool,
		ServerName: "127.0.0.1",
		NextProtos: []string{"http/1.1"},
	})
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"secure":true}`, string(body))

	entry := lastEntry(t, s.Store())
	require.NotNil(t, entry)
	assert.Equal(t, "https", entry.Scheme)
	assert.Equal(t, "/secure", entry.Path)
	require.NotNil(t, entry.ResponseBody)
	assert.JSONEq(t, `{"secure":true}`, *entry.ResponseBody)
}

func mustPort(t *testing.T, u *url.URL) int {
	t.Helper()
	_, port, err := net.SplitHostPort(u.Host)
	require.NoError(t, err)
	p, err := strconv.Atoi(port)
	require.NoError(t, err)
	return p
}
