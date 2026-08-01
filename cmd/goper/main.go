package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/romayengineer/goper/internal/api"
	"github.com/romayengineer/goper/internal/config"
	"github.com/romayengineer/goper/internal/iptables"
	"github.com/romayengineer/goper/internal/output"
	"github.com/romayengineer/goper/internal/proxy"
)

func main() {
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(run(cfg))
}

// run executes the full goper lifecycle: logging setup, proxy + API server
// startup, optional transparent iptables management, and graceful shutdown on
// SIGINT/SIGTERM. It returns the process exit code. Extracted from main so it
// is unit-testable; server goroutines still os.Exit(1) on fatal serve errors,
// matching the previous behavior (a serving failure is unrecoverable).
func run(cfg *config.Config) int {
	setupLogging(cfg)

	slog.Info("starting",
		"port", cfg.Port,
		"api_port", cfg.APIPort,
		"transparent", cfg.Transparent,
		"log_format", cfg.LogFormat,
	)

	proxyServer, err := proxy.NewServer(cfg)
	if err != nil {
		slog.Error("create proxy server", "error", err)
		return 1
	}

	if cfg.OutputDir != "" {
		wireOutputs(proxyServer, cfg)
	}

	var iptMgr iptables.RuleManager = iptables.NewManager(cfg.ProxyPort(), nil)

	if cfg.Transparent {
		if err := checkTransparentPrivileges(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := iptMgr.Setup(); err != nil {
			slog.Error("setup iptables", "error", err)
			return 1
		}
		slog.Info("transparent proxy rules installed")
	}

	caPEM := readCAPEM(cfg)

	apiServer := api.NewServer(cfg.APIPort, proxyServer.Store(), caPEM)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

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
	return 0
}

// setupLogging installs the process-wide slog handler according to the
// configured format (text or json) and level.
func setupLogging(cfg *config.Config) {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	var h slog.Handler
	if cfg.LogFormat == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}

// wireOutputs attaches the configured on-disk capture writer (json or ndjson)
// to the proxy server.
func wireOutputs(proxyServer proxy.Runnable, cfg *config.Config) {
	if cfg.OutputFormat == "ndjson" {
		proxyServer.AddOutput(output.NewNDJSONBodyWriter(cfg.OutputDir))
	} else {
		proxyServer.AddOutput(output.NewJSONBodyWriter(cfg.OutputDir))
	}
	slog.Info("JSON output enabled", "dir", cfg.OutputDir, "format", cfg.OutputFormat)
}

// checkTransparentPrivileges verifies the process can manage iptables
// (transparent mode prerequisite).
func checkTransparentPrivileges() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("transparent mode requires running as root with CAP_NET_ADMIN\nhint: rebuild with `docker compose up --build` (cap_add: NET_ADMIN) or run goper as root")
	}
	return nil
}

// readCAPEM loads the CA certificate PEM for the API download endpoint. A
// missing/unreadable cert yields nil (the endpoint then returns 404).
func readCAPEM(cfg *config.Config) []byte {
	certPath := filepath.Join(cfg.CADir, "ca-cert.pem")
	if data, err := os.ReadFile(certPath); err == nil {
		return data
	}
	return nil
}

// parseConfig parses command-line args into a *config.Config, applying flag
// defaults, validation (output format, capture regexes), derived settings
// (verbose → debug level, --log-format json) and ~ expansion in --ca-dir.
// Extracted from main so it is unit-testable; main exits with code 2 on error.
func parseConfig(args []string) (*config.Config, error) {
	cfg := config.Default()
	logFormat := ""

	fs := flag.NewFlagSet("goper", flag.ContinueOnError)
	fs.IntVar(&cfg.Port, "port", cfg.Port, "proxy listen port")
	fs.IntVar(&cfg.APIPort, "api-port", cfg.APIPort, "API server port")
	fs.StringVar(&cfg.CADir, "ca-dir", cfg.CADir, "CA certificate directory")
	fs.BoolVar(&cfg.Transparent, "transparent", cfg.Transparent, "enable transparent proxy mode (requires iptables + CAP_NET_ADMIN)")
	fs.BoolVar(&cfg.Verbose, "verbose", cfg.Verbose, "verbose logging (sets log level to debug)")
	fs.IntVar(&cfg.BufferSize, "buffer", cfg.BufferSize, "ring buffer size")
	fs.StringVar(&logFormat, "log-format", "text", "log format (text or json)")
	fs.StringVar(&cfg.OutputDir, "output-dir", cfg.OutputDir, "directory to write JSON response bodies (empty = disabled)")
	fs.StringVar(&cfg.OutputFormat, "output-format", cfg.OutputFormat, "output format (json or ndjson)")
	fs.Int64Var(&cfg.RequestBodyLimit, "request-body-limit", cfg.RequestBodyLimit, "max request body bytes to capture (0 = unlimited)")
	fs.Int64Var(&cfg.ResponseBodyLimit, "response-body-limit", cfg.ResponseBodyLimit, "max response body bytes to capture (0 = unlimited)")
	fs.StringVar(&cfg.CaptureInclude, "capture-include", cfg.CaptureInclude, "only capture URLs matching this regex")
	fs.StringVar(&cfg.CaptureExclude, "capture-exclude", cfg.CaptureExclude, "never capture URLs matching this regex")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if cfg.OutputFormat != "json" && cfg.OutputFormat != "ndjson" {
		return nil, fmt.Errorf("invalid --output-format: must be 'json' or 'ndjson'")
	}
	if cfg.CaptureInclude != "" {
		if _, err := regexp.Compile(cfg.CaptureInclude); err != nil {
			return nil, fmt.Errorf("invalid --capture-include regex: %w", err)
		}
	}
	if cfg.CaptureExclude != "" {
		if _, err := regexp.Compile(cfg.CaptureExclude); err != nil {
			return nil, fmt.Errorf("invalid --capture-exclude regex: %w", err)
		}
	}

	if cfg.Verbose {
		cfg.LogLevel = slog.LevelDebug
	}
	if logFormat == "json" {
		cfg.LogFormat = "json"
	}

	if home, err := os.UserHomeDir(); err == nil {
		cfg.CADir = strings.Replace(cfg.CADir, "~", home, 1)
	}

	return cfg, nil
}
