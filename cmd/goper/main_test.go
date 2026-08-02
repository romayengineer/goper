package main

import (
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/romayengineer/goper/internal/config"
	"github.com/romayengineer/goper/internal/proxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := parseConfig(nil)
	require.NoError(t, err)

	d := config.Default()
	assert.Equal(t, d.Port, cfg.Port)
	assert.Equal(t, d.APIPort, cfg.APIPort)
	assert.False(t, cfg.Transparent)
	assert.Equal(t, d.BufferSize, cfg.BufferSize)
	assert.Equal(t, "text", cfg.LogFormat)
	assert.Equal(t, d.ResponseBodyLimit, cfg.ResponseBodyLimit)
	// The default CA dir is expanded (~ → $HOME) during parsing.
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".goper", "ca"), cfg.CADir)
}

func TestParseConfigSetsFlags(t *testing.T) {
	cfg, err := parseConfig([]string{
		"--port", "9999",
		"--api-port", "9998",
		"--ca-dir", "/tmp/custom-ca",
		"--transparent",
		"--buffer", "42",
		"--output-dir", "/tmp/out",
		"--output-format", "ndjson",
		"--request-body-limit", "1024",
		"--response-body-limit", "2048",
		"--capture-include", `\.json$`,
		"--capture-exclude", `ads`,
	})
	require.NoError(t, err)

	assert.Equal(t, 9999, cfg.Port)
	assert.Equal(t, 9998, cfg.APIPort)
	assert.Equal(t, "/tmp/custom-ca", cfg.CADir)
	assert.True(t, cfg.Transparent)
	assert.Equal(t, 42, cfg.BufferSize)
	assert.Equal(t, "/tmp/out", cfg.OutputDir)
	assert.Equal(t, "ndjson", cfg.OutputFormat)
	assert.Equal(t, int64(1024), cfg.RequestBodyLimit)
	assert.Equal(t, int64(2048), cfg.ResponseBodyLimit)
	assert.Equal(t, `\.json$`, cfg.CaptureInclude)
	assert.Equal(t, "ads", cfg.CaptureExclude)
}

func TestParseConfigInvalidOutputFormat(t *testing.T) {
	_, err := parseConfig([]string{"--output-format", "xml"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "output-format")
}

func TestParseConfigInvalidIncludeRegex(t *testing.T) {
	_, err := parseConfig([]string{"--capture-include", "[unclosed"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "capture-include")
}

func TestParseConfigInvalidExcludeRegex(t *testing.T) {
	_, err := parseConfig([]string{"--capture-exclude", "[unclosed"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "capture-exclude")
}

func TestParseConfigUnknownFlag(t *testing.T) {
	_, err := parseConfig([]string{"--bogus-flag"})
	require.Error(t, err)
}

func TestParseConfigVerboseSetsDebugLevel(t *testing.T) {
	cfg, err := parseConfig([]string{"--verbose"})
	require.NoError(t, err)
	assert.Equal(t, slog.LevelDebug, cfg.LogLevel)
}

func TestParseConfigLogFormatJSON(t *testing.T) {
	cfg, err := parseConfig([]string{"--log-format", "json"})
	require.NoError(t, err)
	assert.Equal(t, "json", cfg.LogFormat)
}

func TestParseConfigTildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	cfg, err := parseConfig([]string{"--ca-dir", "~/goper/ca"})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "goper", "ca"), cfg.CADir)
}

func TestParseConfigTildeExpansionOnlyFirst(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	cfg, err := parseConfig([]string{"--ca-dir", "~/a~/b"})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "a~/b"), cfg.CADir, "only the leading ~ is expanded")
}

// ---- run() lifecycle tests ----

func TestRunNewServerError(t *testing.T) {
	// CA dir under a regular file: LoadOrCreateCA fails → run returns 1
	// before starting any servers or waiting on signals.
	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644)) // #nosec G306 -- test fixture; not sensitive

	cfg := config.Default()
	cfg.CADir = filepath.Join(blocker, "sub")

	assert.Equal(t, 1, run(cfg), "proxy server creation failure must exit 1")
}

func TestRunTransparentNonRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires a non-root user for the privilege check")
	}

	cfg := config.Default()
	cfg.CADir = t.TempDir()
	cfg.Transparent = true

	assert.Equal(t, 1, run(cfg), "transparent mode as non-root must fail fast")
}

// TestRunGracefulShutdown starts the full lifecycle (both servers on
// ephemeral ports) and verifies SIGTERM triggers a clean shutdown with exit
// code 0.
func TestRunGracefulShutdown(t *testing.T) {
	// Guard channel: absorb any SIGTERM sent before run() registers its own
	// handler, so the test process can never die from our own signal.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGTERM)
	defer signal.Stop(guard)

	cfg := config.Default()
	cfg.CADir = t.TempDir()
	cfg.Port = 0 // ephemeral
	cfg.APIPort = 0
	cfg.BufferSize = 10

	stop := make(chan struct{})
	defer close(stop)
	go signalSIGTERMUntilClosed(stop)

	done := make(chan int, 1)
	go func() { done <- run(cfg) }()

	select {
	case code := <-done:
		assert.Equal(t, 0, code, "graceful shutdown after SIGTERM must exit 0")
	case <-time.After(15 * time.Second):
		t.Fatal("run did not shut down after SIGTERM")
	}
}

// signalSIGTERMUntilClosed keeps sending SIGTERM to this process until stop is
// closed.
func signalSIGTERMUntilClosed(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		time.Sleep(100 * time.Millisecond)
	}
}

func TestSetupLoggingJSON(t *testing.T) {
	cfg := config.Default()
	cfg.LogFormat = "json"
	setupLogging(cfg)
	_, ok := slog.Default().Handler().(*slog.JSONHandler)
	assert.True(t, ok, "expected a JSON handler after setupLogging with json format")
}

func TestSetupLoggingText(t *testing.T) {
	cfg := config.Default()
	cfg.LogFormat = "text"
	setupLogging(cfg)
	_, ok := slog.Default().Handler().(*slog.TextHandler)
	assert.True(t, ok, "expected a text handler after setupLogging with text format")
}

func TestCheckTransparentPrivileges(t *testing.T) {
	err := checkTransparentPrivileges()
	if os.Geteuid() == 0 {
		assert.NoError(t, err, "root passes the privilege check")
		return
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "root")
	assert.Contains(t, err.Error(), "CAP_NET_ADMIN")
}

func TestReadCAPEMFound(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ca-cert.pem"), []byte("PEMDATA"), 0o644)) // #nosec G306 -- test CA cert; public by design

	cfg := config.Default()
	cfg.CADir = dir
	assert.Equal(t, []byte("PEMDATA"), readCAPEM(cfg))
}

func TestReadCAPEMMissing(t *testing.T) {
	cfg := config.Default()
	cfg.CADir = t.TempDir()
	assert.Nil(t, readCAPEM(cfg), "missing cert must yield nil (API returns 404)")
}

func TestWireOutputsJSONAndNDJSON(t *testing.T) {
	dir := t.TempDir()

	for _, format := range []string{"json", "ndjson"} {
		cfg := config.Default()
		cfg.CADir = t.TempDir()
		cfg.OutputDir = dir
		cfg.OutputFormat = format

		s, err := proxy.NewServer(cfg)
		require.NoError(t, err)

		require.NotPanics(t, func() { wireOutputs(s, cfg) }, "wireOutputs %s", format)
	}
}
