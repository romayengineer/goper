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

	setupOutputs(proxyServer, cfg)

	var iptMgr iptables.RuleManager = iptables.NewManager(cfg.ProxyPort(), nil)

	if code := setupTransparentIfEnabled(cfg, iptMgr); code != 0 {
		return code
	}

	caPEM := readCAPEM(cfg)

	apiServer := api.NewServer(cfg.APIPort, proxyServer.Store(), caPEM)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go runServer("proxy server error", proxyServer)
	go runServer("API server error", apiServer)

	logReady(cfg, caPEM)

	sig := <-sigCh
	slog.Info("shutting down", "signal", sig)

	shutdownTransparent(cfg, iptMgr)

	slog.Info("stopped")
	return 0
}

// logReady reports that the servers are up and, when a CA certificate is
// available, where to download it.
func logReady(cfg *config.Config, caPEM []byte) {
	slog.Info("ready",
		"proxy", cfg.Port,
		"api", cfg.APIPort,
	)
	if caPEM != nil {
		slog.Info("download CA cert",
			"url", api.CAURL(cfg.APIPort),
		)
	}
}

// serverRunner is satisfied by both the proxy and API servers.
type serverRunner interface {
	Run() error
}

// runServer runs a server in a goroutine, exiting the process on failure.
func runServer(name string, server serverRunner) {
	if err := server.Run(); err != nil {
		slog.Error(name, "error", err)
		os.Exit(1)
	}
}

// setupTransparentIfEnabled installs iptables rules for transparent mode when
// enabled. Returns a non-zero exit code on failure.
func setupTransparentIfEnabled(cfg *config.Config, iptMgr iptables.RuleManager) int {
	if !cfg.Transparent {
		return 0
	}
	if err := installRules(iptMgr); err != nil {
		return 1
	}
	slog.Info("transparent proxy rules installed")
	return 0
}

// installRules verifies privileges and installs iptables rules, logging any
// failure.
func installRules(iptMgr iptables.RuleManager) error {
	if err := checkTransparentPrivileges(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return err
	}
	if err := iptMgr.Setup(); err != nil {
		slog.Error("setup iptables", "error", err)
		return err
	}
	return nil
}

// teardownTransparent removes the iptables rules on shutdown.
func teardownTransparent(iptMgr iptables.RuleManager) {
	if err := iptMgr.Teardown(); err != nil {
		slog.Error("remove iptables rules", "error", err)
	}
}

// shutdownTransparent tears down transparent-mode iptables rules at shutdown,
// but only when transparent mode was enabled.
func shutdownTransparent(cfg *config.Config, iptMgr iptables.RuleManager) {
	if !cfg.Transparent {
		return
	}
	teardownTransparent(iptMgr)
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

// setupOutputs wires the on-disk capture writer. JSON capture is enabled by
// default (--output-dir defaults to "captures"); --no-capture disables it,
// and an explicitly empty --output-dir also disables it.
func setupOutputs(proxyServer proxy.Runnable, cfg *config.Config) {
	if cfg.NoCapture {
		slog.Info("JSON output disabled (--no-capture)")
		return
	}
	if cfg.OutputDir != "" {
		wireOutputs(proxyServer, cfg)
	}
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
	if data, err := os.ReadFile(certPath); err == nil { // #nosec G304,G703 -- path derives from the configured CA dir
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
	fs.StringVar(&cfg.OutputDir, "output-dir", cfg.OutputDir, "directory to write JSON response bodies (default: captures; empty disables)")
	fs.StringVar(&cfg.OutputFormat, "output-format", cfg.OutputFormat, "output format (json or ndjson)")
	fs.BoolVar(&cfg.NoCapture, "no-capture", cfg.NoCapture, "disable writing JSON captures to disk (keeps the live dashboard)")
	fs.Int64Var(&cfg.RequestBodyLimit, "request-body-limit", cfg.RequestBodyLimit, "max request body bytes to capture (0 = unlimited)")
	fs.Int64Var(&cfg.ResponseBodyLimit, "response-body-limit", cfg.ResponseBodyLimit, "max response body bytes to capture (0 = unlimited)")
	fs.StringVar(&cfg.CaptureInclude, "capture-include", cfg.CaptureInclude, "only capture URLs matching this regex")
	fs.StringVar(&cfg.CaptureExclude, "capture-exclude", cfg.CaptureExclude, "never capture URLs matching this regex")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	applyDerivedSettings(cfg, logFormat)
	cfg.CADir = expandHomeDir(cfg.CADir)

	return cfg, nil
}

// applyDerivedSettings sets the verbose-derived log level and the requested
// --log-format.
func applyDerivedSettings(cfg *config.Config, logFormat string) {
	if cfg.Verbose {
		cfg.LogLevel = slog.LevelDebug
	}
	if logFormat == "json" {
		cfg.LogFormat = "json"
	}
}

// expandHomeDir replaces a leading ~ in a path with the user's home directory.
func expandHomeDir(path string) string {
	if home, err := os.UserHomeDir(); err == nil {
		return strings.Replace(path, "~", home, 1)
	}
	return path
}

// validateConfig checks output format and capture regexes after flag parsing.
func validateConfig(cfg *config.Config) error {
	if !validOutputFormat(cfg.OutputFormat) {
		return fmt.Errorf("invalid --output-format: must be 'json' or 'ndjson'")
	}
	if err := validateCaptureRegex("--capture-include", cfg.CaptureInclude); err != nil {
		return err
	}
	return validateCaptureRegex("--capture-exclude", cfg.CaptureExclude)
}

func validOutputFormat(f string) bool {
	return f == "json" || f == "ndjson"
}

// validateCaptureRegex compiles a capture filter regex, skipping empty ones.
func validateCaptureRegex(name, pattern string) error {
	if pattern == "" {
		return nil
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("invalid %s regex: %w", name, err)
	}
	return nil
}
