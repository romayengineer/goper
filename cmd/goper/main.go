package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/romayengineer/goper/internal/api"
	"github.com/romayengineer/goper/internal/config"
	"github.com/romayengineer/goper/internal/iptables"
	"github.com/romayengineer/goper/internal/output"
	"github.com/romayengineer/goper/internal/proxy"
)

func main() {
	cfg := config.Default()
	logFormat := ""

	flag.IntVar(&cfg.Port, "port", cfg.Port, "proxy listen port")
	flag.IntVar(&cfg.APIPort, "api-port", cfg.APIPort, "API server port")
	flag.StringVar(&cfg.CADir, "ca-dir", cfg.CADir, "CA certificate directory")
	flag.BoolVar(&cfg.Transparent, "transparent", cfg.Transparent, "enable transparent proxy mode (requires iptables + CAP_NET_ADMIN)")
	flag.BoolVar(&cfg.Verbose, "verbose", cfg.Verbose, "verbose logging (sets log level to debug)")
	flag.IntVar(&cfg.BufferSize, "buffer", cfg.BufferSize, "ring buffer size")
	flag.StringVar(&logFormat, "log-format", "text", "log format (text or json)")
	flag.StringVar(&cfg.OutputDir, "output-dir", cfg.OutputDir, "directory to write JSON response bodies (empty = disabled)")
	flag.StringVar(&cfg.OutputFormat, "output-format", cfg.OutputFormat, "output format (json or ndjson)")
	flag.Parse()

	if cfg.OutputFormat != "json" && cfg.OutputFormat != "ndjson" {
		fmt.Fprintln(os.Stderr, "invalid --output-format: must be 'json' or 'ndjson'")
		os.Exit(2)
	}

	if cfg.Verbose {
		cfg.LogLevel = slog.LevelDebug
	}
	if logFormat == "json" {
		cfg.LogFormat = "json"
	}

	var h slog.Handler
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))

	if home, err := os.UserHomeDir(); err == nil {
		cfg.CADir = strings.Replace(cfg.CADir, "~", home, 1)
	}

	slog.Info("starting",
		"port", cfg.Port,
		"api_port", cfg.APIPort,
		"transparent", cfg.Transparent,
		"log_format", cfg.LogFormat,
	)

	proxyServer, err := proxy.NewServer(cfg)
	if err != nil {
		slog.Error("create proxy server", "error", err)
		os.Exit(1)
	}

	if cfg.OutputDir != "" {
		if cfg.OutputFormat == "ndjson" {
			proxyServer.AddOutput(output.NewNDJSONBodyWriter(filepath.Join(cfg.OutputDir, "responses.jsonl")))
		} else {
			proxyServer.AddOutput(output.NewJSONBodyWriter(cfg.OutputDir))
		}
		slog.Info("JSON output enabled", "dir", cfg.OutputDir, "format", cfg.OutputFormat)
	}

	var iptMgr iptables.RuleManager = iptables.NewManager(cfg.ProxyPort(), nil)

	if cfg.Transparent {
		if err := iptMgr.Setup(); err != nil {
			slog.Error("setup iptables", "error", err)
			os.Exit(1)
		}
		slog.Info("transparent proxy rules installed")
	}

	var caPEM []byte
	certPath := filepath.Join(cfg.CADir, "ca-cert.pem")
	if data, err := os.ReadFile(certPath); err == nil {
		caPEM = data
	}

	apiServer := api.NewServer(cfg.APIPort, proxyServer.Store(), caPEM)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := proxyServer.Run(); err != nil {
			slog.Error("proxy server error", "error", err)
			os.Exit(1)
		}
	}()

	go func() {
		if err := apiServer.Run(); err != nil {
			slog.Error("API server error", "error", err)
			os.Exit(1)
		}
	}()

	slog.Info("ready",
		"proxy", cfg.Port,
		"api", cfg.APIPort,
	)
	if caPEM != nil {
		slog.Info("download CA cert",
			"url", api.CAURL(cfg.APIPort),
		)
	}

	sig := <-sigCh
	slog.Info("shutting down", "signal", sig)

	if cfg.Transparent {
		if err := iptMgr.Teardown(); err != nil {
			slog.Error("remove iptables rules", "error", err)
		}
	}

	slog.Info("stopped")
}
