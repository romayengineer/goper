//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"io"
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

// startRun runs the real binary lifecycle (parseConfig → NewServer →
// setupOutputs → serve) with cfg in a goroutine and waits until the proxy
// accepts connections, returning run()'s exit channel and the proxy address.
// A guard channel absorbs our own SIGTERM so the test process can never die.
func startRun(t *testing.T, cfg *config.Config) (done chan int, proxyAddr string) {
	t.Helper()
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGTERM)
	t.Cleanup(func() { signal.Stop(guard) })

	done = make(chan int, 1)
	go func() { done <- run(cfg) }()

	proxyAddr = fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", proxyAddr, time.Second)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 10*time.Second, 50*time.Millisecond, "proxy to start listening")
	return done, proxyAddr
}

// stopRun sends SIGTERM and requires run() to shut down cleanly with exit 0.
func stopRun(t *testing.T, done chan int) {
	t.Helper()
	_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	select {
	case code := <-done:
		assert.Equal(t, 0, code, "graceful shutdown after SIGTERM must exit 0")
	case <-time.After(10 * time.Second):
		t.Fatal("run did not shut down after SIGTERM")
	}
}

// proxiedClient returns an HTTP client that routes through proxyAddr.
func proxiedClient(proxyAddr string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: proxyAddr}),
	}}
}

// hasCaptureFile reports whether any .json file exists under dir (captures are
// organized into per-domain subdirectories).
func hasCaptureFile(t *testing.T, dir string) bool {
	t.Helper()
	dirs, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, d.Name()))
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
}

// statsCount queries the API and returns the captured-request count.
func statsCount(t *testing.T, apiPort int) int {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/stats", apiPort))
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var stats struct {
		Count int `json:"count"`
	}
	require.NoError(t, json.Unmarshal(body, &stats))
	return stats.Count
}

// TestRunWritesCaptureFilesWithOutputDir drives a request through the proxy and
// asserts the JSON capture lands in an explicit --output-dir. It also exercises
// the "any body that parses as JSON is dumped" behavior end to end.
func TestRunWritesCaptureFilesWithOutputDir(t *testing.T) {
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

	done, proxyAddr := startRun(t, cfg)
	resp, err := proxiedClient(proxyAddr).Get(upstream.URL + "/api/data")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	assert.Eventually(t, func() bool {
		return hasCaptureFile(t, outDir)
	}, 5*time.Second, 50*time.Millisecond, "capture file to appear in --output-dir")

	stopRun(t, done)
}

// TestRunWritesCaptureFilesByDefault verifies the new default: with no
// --output-dir and no --no-capture, JSON captures land in ./captures relative
// to the working directory.
func TestRunWritesCaptureFilesByDefault(t *testing.T) {
	t.Chdir(t.TempDir())

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"default":true}`))
	}))
	defer upstream.Close()

	cfg := config.Default()
	cfg.Port = freePort(t)
	cfg.APIPort = freePort(t)
	cfg.CADir = t.TempDir()
	cfg.BufferSize = 100
	// OutputDir intentionally left at the default ("captures").

	done, proxyAddr := startRun(t, cfg)
	resp, err := proxiedClient(proxyAddr).Get(upstream.URL + "/api/data")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	assert.Eventually(t, func() bool {
		return hasCaptureFile(t, "captures")
	}, 5*time.Second, 50*time.Millisecond, "default capture to appear in ./captures")

	stopRun(t, done)
}

// TestRunNoCaptureWritesNoFiles verifies --no-capture disables on-disk JSON
// writing while the in-memory store (the live dashboard) still captures.
func TestRunNoCaptureWritesNoFiles(t *testing.T) {
	t.Chdir(t.TempDir())

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nocapture":true}`))
	}))
	defer upstream.Close()

	cfg := config.Default()
	cfg.Port = freePort(t)
	cfg.APIPort = freePort(t)
	cfg.CADir = t.TempDir()
	cfg.BufferSize = 100
	cfg.NoCapture = true

	done, proxyAddr := startRun(t, cfg)
	resp, err := proxiedClient(proxyAddr).Get(upstream.URL + "/api/data")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	// Give any (incorrect) writer a moment before asserting nothing landed.
	time.Sleep(300 * time.Millisecond)
	_, err = os.Stat("captures")
	assert.True(t, os.IsNotExist(err), "--no-capture must not create a captures dir")

	assert.Eventually(t, func() bool {
		return statsCount(t, cfg.APIPort) > 0
	}, 5*time.Second, 50*time.Millisecond, "ring buffer still captures with --no-capture")

	stopRun(t, done)
}
