package config

import (
	"log/slog"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()

	if cfg.Port != 8080 {
		t.Fatalf("port: got %d, want 8080", cfg.Port)
	}
	if cfg.APIPort != 8081 {
		t.Fatalf("api port: got %d, want 8081", cfg.APIPort)
	}
	if cfg.CADir != "~/.goper/ca" {
		t.Fatalf("ca dir: got %q", cfg.CADir)
	}
	if cfg.Transparent {
		t.Fatal("expected transparent=false")
	}
	if cfg.Verbose {
		t.Fatal("expected verbose=false")
	}
	if cfg.BufferSize != 10000 {
		t.Fatalf("buffer size: got %d, want 10000", cfg.BufferSize)
	}
	if cfg.LogFormat != "text" {
		t.Fatalf("log format: got %q, want text", cfg.LogFormat)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("log level: got %v, want info", cfg.LogLevel)
	}
}

func TestConfigImplementsProvider(t *testing.T) {
	var _ Provider = Default()
}

func TestProviderMethods(t *testing.T) {
	cfg := &Config{
		Port:        1234,
		APIPort:     5678,
		CADir:       "/tmp/ca",
		Transparent: true,
		Verbose:     true,
		BufferSize:  500,
		LogFormat:   "json",
		LogLevel:    slog.LevelDebug,
	}

	if cfg.ProxyPort() != 1234 {
		t.Fatalf("ProxyPort: got %d", cfg.ProxyPort())
	}
	if cfg.GetAPIPort() != 5678 {
		t.Fatalf("GetAPIPort: got %d", cfg.GetAPIPort())
	}
	if cfg.GetCADir() != "/tmp/ca" {
		t.Fatalf("GetCADir: got %q", cfg.GetCADir())
	}
	if !cfg.IsTransparent() {
		t.Fatal("IsTransparent: want true")
	}
	if !cfg.IsVerbose() {
		t.Fatal("IsVerbose: want true")
	}
	if cfg.GetBufferSize() != 500 {
		t.Fatalf("GetBufferSize: got %d", cfg.GetBufferSize())
	}
	if cfg.GetLogFormat() != "json" {
		t.Fatalf("GetLogFormat: got %q", cfg.GetLogFormat())
	}
	if cfg.GetLogLevel() != slog.LevelDebug {
		t.Fatalf("GetLogLevel: got %v", cfg.GetLogLevel())
	}
}

func TestProviderMethodsZeroValues(t *testing.T) {
	cfg := &Config{}

	if cfg.ProxyPort() != 0 {
		t.Fatal("expected zero-value port")
	}
	if cfg.GetCADir() != "" {
		t.Fatal("expected empty ca dir")
	}
	if cfg.IsTransparent() {
		t.Fatal("expected transparent=false for zero value")
	}
	if cfg.GetLogLevel() != 0 {
		t.Fatal("expected zero log level")
	}
}
