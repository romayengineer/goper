package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/romayengineer/goper/internal/config"
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
