//go:build integration

package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romayengineer/goper/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countJSONFiles returns the number of .json files under dir (recursively),
// matching how captures are organized into per-domain subdirectories.
func countJSONFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			n++
		}
		return nil
	})
	return n
}

// TestNativeForwardProxyWritesCaptureFiles reproduces the RUNNING_NATIVE.md
// workflow without Docker: goper as a plain forward proxy (no --transparent)
// with a real on-disk JSONBodyWriter. It drives plain HTTP and HTTPS
// (CONNECT + MITM) requests through the proxy and asserts the pretty .json
// captures land in <output-dir>/<domain>/.
func TestNativeForwardProxyWritesCaptureFiles(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer upstream.Close()

	tlsUpstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"secure":true}`))
	}))
	defer tlsUpstream.Close()

	outDir := t.TempDir()

	s := newTestServer(t, testConfig(t))
	s.AddOutput(output.NewJSONBodyWriter(outDir))

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
	proxyAddr := ln.Addr().String()

	// 1. Plain HTTP through the forward proxy.
	resp, err := proxyClient(t, "http://"+proxyAddr, nil).Get(upstream.URL + "/api/data")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	// 2. HTTPS through the proxy: goproxy MITMs with goper's CA, so the client
	//    must trust it; the outbound transport must trust the httptest TLS cert.
	upstreamPool := x509.NewCertPool()
	upstreamPool.AddCert(tlsUpstream.Certificate())
	s.proxy.Tr = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: upstreamPool}}

	clientPool := x509.NewCertPool()
	clientPool.AddCert(s.CA().Certificate())
	resp2, err := proxyClient(t, "http://"+proxyAddr, clientPool).Get(tlsUpstream.URL + "/secure")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
	require.NoError(t, resp2.Body.Close())

	// Both captures must be recorded in the store and dumped to disk.
	entry := waitForEntry(t, s.Store())
	require.NotNil(t, entry)
	assert.Eventually(t, func() bool {
		return countJSONFiles(t, outDir) >= 2
	}, 5*time.Second, 10*time.Millisecond, "expected HTTP + HTTPS captures on disk")
}

// TestNativeForwardProxyDumpsJSONBodyWithoutJSONContentType covers the case
// where a server serves a JSON payload with a non-JSON (or missing)
// Content-Type. The body is captured in the store but used to be silently
// skipped by the on-disk writer; any body that parses as JSON must now be
// dumped regardless of its Content-Type.
func TestNativeForwardProxyDumpsJSONBodyWithoutJSONContentType(t *testing.T) {
	plainUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`{"nobody":"expects"}`))
	}))
	defer plainUpstream.Close()

	noCTUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"missing":"content-type"}`))
	}))
	defer noCTUpstream.Close()

	outDir := t.TempDir()

	s := newTestServer(t, testConfig(t))
	s.AddOutput(output.NewJSONBodyWriter(outDir))

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
	proxyAddr := ln.Addr().String()
	client := proxyClient(t, "http://"+proxyAddr, nil)

	resp, err := client.Get(plainUpstream.URL + "/api")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	resp, err = client.Get(noCTUpstream.URL + "/api")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	assert.Eventually(t, func() bool {
		return countJSONFiles(t, outDir) >= 2
	}, 5*time.Second, 10*time.Millisecond, "JSON bodies must be dumped regardless of Content-Type")
}
