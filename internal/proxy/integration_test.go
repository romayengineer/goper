package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/romayengineer/goper/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestReadBodyNil(t *testing.T) {
	got, err := readBody(&http.Response{Body: nil})
	assert.NoError(t, err)
	assert.Nil(t, got)
}

func TestReadBodyEmpty(t *testing.T) {
	got, err := readBody(&http.Response{Body: io.NopCloser(strings.NewReader(""))})
	assert.NoError(t, err)
	assert.Empty(t, got)
}

func TestReadBodyContent(t *testing.T) {
	got, err := readBody(&http.Response{Body: io.NopCloser(strings.NewReader(`{"a":1}`))})
	assert.NoError(t, err)
	assert.Equal(t, `{"a":1}`, string(got))
}
