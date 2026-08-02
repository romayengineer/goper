package config

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefault(t *testing.T) {
	cfg := Default()

	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, 8081, cfg.APIPort)
	assert.Equal(t, "~/.goper/ca", cfg.CADir)
	assert.False(t, cfg.Transparent)
	assert.False(t, cfg.Verbose)
	assert.Equal(t, 10000, cfg.BufferSize)
	assert.Equal(t, "text", cfg.LogFormat)
	assert.Equal(t, slog.LevelInfo, cfg.LogLevel)
	assert.Equal(t, "captures", cfg.OutputDir, "JSON capture to disk is on by default")
	assert.Equal(t, "json", cfg.OutputFormat)
	assert.False(t, cfg.NoCapture, "capture is enabled by default")
	assert.Zero(t, cfg.RequestBodyLimit, "request body limit defaults to unlimited")
	assert.Equal(t, int64(1<<20), cfg.ResponseBodyLimit, "response body limit defaults to 1 MiB")
	assert.Empty(t, cfg.CaptureInclude)
	assert.Empty(t, cfg.CaptureExclude)
}

func TestConfigImplementsProvider(t *testing.T) {
	var _ Provider = Default()
}

func TestProviderMethods(t *testing.T) {
	cfg := &Config{
		Port:              1234,
		APIPort:           5678,
		CADir:             "/tmp/ca",
		Transparent:       true,
		Verbose:           true,
		BufferSize:        500,
		LogFormat:         "json",
		LogLevel:          slog.LevelDebug,
		OutputDir:         "/tmp/out",
		OutputFormat:      "ndjson",
		RequestBodyLimit:  1024,
		ResponseBodyLimit: 2048,
		CaptureInclude:    `\.json$`,
		CaptureExclude:    `ads?\.`,
	}

	assert.Equal(t, 1234, cfg.ProxyPort())
	assert.Equal(t, 5678, cfg.GetAPIPort())
	assert.Equal(t, "/tmp/ca", cfg.GetCADir())
	assert.True(t, cfg.IsTransparent())
	assert.True(t, cfg.IsVerbose())
	assert.Equal(t, 500, cfg.GetBufferSize())
	assert.Equal(t, "json", cfg.GetLogFormat())
	assert.Equal(t, slog.LevelDebug, cfg.GetLogLevel())
	assert.Equal(t, "/tmp/out", cfg.GetOutputDir())
	assert.Equal(t, "ndjson", cfg.GetOutputFormat())
	assert.Equal(t, int64(1024), cfg.GetRequestBodyLimit())
	assert.Equal(t, int64(2048), cfg.GetResponseBodyLimit())
	assert.Equal(t, `\.json$`, cfg.GetCaptureInclude())
	assert.Equal(t, `ads?\.`, cfg.GetCaptureExclude())
}

func TestProviderMethodsZeroValues(t *testing.T) {
	cfg := &Config{}

	assert.Zero(t, cfg.ProxyPort())
	assert.Empty(t, cfg.GetCADir())
	assert.False(t, cfg.IsTransparent())
	assert.Zero(t, cfg.GetLogLevel())
	assert.Empty(t, cfg.GetOutputDir())
	assert.Empty(t, cfg.GetOutputFormat())
}
