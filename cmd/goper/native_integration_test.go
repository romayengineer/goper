//go:build integration

package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/romayengineer/goper/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freePort reserves a free TCP port on loopback and releases it, returning the
// number so a config can bind it.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestRunWritesCaptureFilesWithOutputDir runs the real binary lifecycle
// (parseConfig → NewServer → setupOutputs → serve) natively with --output-dir
// set, drives a request through the proxy, and asserts the JSON capture lands
// on disk. This guards the wiring between the CLI flags and the on-disk writer
// — a gap that left the dashboard populated while ./captures stayed empty.
func TestRunWritesCaptureFilesWithOutputDir(t *testing.T) {
	// Serve a JSON payload with a non-JSON Content-Type to also exercise the
	// "any body that parses as JSON is dumped" behavior end to end.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`{"wired":true}`))
	}))
	defer upstream.Close()

	outDir := t.TempDir()

	cfg := config.Default()
	cfg.Port = freePort(t)
	cfg.APIPort = freePort(t)
	cfg.CADir = t.TempDir()
	cfg.OutputDir = outDir
	cfg.BufferSize = 100

	// Guard channel: absorb any SIGTERM sent before run() registers its own
	// handler, so the test process can never die from our own signal.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGTERM)
	defer signal.Stop(guard)

	done := make(chan int, 1)
	go func() { done <- run(cfg) }()

	proxyAddr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", proxyAddr, time.Second)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 10*time.Second, 50*time.Millisecond, "proxy to start listening")

	// Drive a request through the proxy, mirroring RUNNING_NATIVE.md's curl step.
	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: proxyAddr}),
	}}
	resp, err := client.Get(upstream.URL + "/api/data")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	// The capture must land on disk under <outDir>/<domain>/.
	assert.Eventually(t, func() bool {
		dirs, err := os.ReadDir(outDir)
		if err != nil {
			return false
		}
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			files, err := os.ReadDir(filepath.Join(outDir, d.Name()))
			if err != nil {
				continue
			}
			for _, f := range files {
				if strings.HasSuffix(f.Name(), ".json") {
					return true
				}
			}
		}
		return false
	}, 5*time.Second, 50*time.Millisecond, "capture file to appear in --output-dir")

	// Graceful shutdown.
	_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	select {
	case code := <-done:
		assert.Equal(t, 0, code, "graceful shutdown after SIGTERM must exit 0")
	case <-time.After(10 * time.Second):
		t.Fatal("run did not shut down after SIGTERM")
	}
}
