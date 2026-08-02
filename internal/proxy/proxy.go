package proxy

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/romayengineer/goper/internal/capture"
	"github.com/romayengineer/goper/internal/config"
	"github.com/romayengineer/goper/internal/output"
)

type Runnable interface {
	Run() error
	RunWithListener(l net.Listener) error
	Store() capture.Store
	CA() CAProvider
	AddOutput(w output.Writer)
}

type Server struct {
	proxy    *goproxy.ProxyHttpServer
	store    capture.Store
	cache    CertStore
	ca       CAProvider
	config   config.Provider
	recorder capture.Recorder
	outputs  []output.Writer

	includeRE *regexp.Regexp
	excludeRE *regexp.Regexp

	resolver OriginalDstResolver
	peeker   SNIPeeker
}

func NewServer(cfg config.Provider) (Runnable, error) {
	ca, err := LoadOrCreateCA(cfg.GetCADir())
	if err != nil {
		return nil, fmt.Errorf("load CA: %w", err)
	}

	store := capture.NewRingBuffer(cfg.GetBufferSize())
	cache := NewCertCache(ca)

	s := &Server{
		proxy:    goproxy.NewProxyHttpServer(),
		store:    store,
		cache:    cache,
		ca:       ca,
		config:   cfg,
		recorder: capture.NewDefaultRecorder(cfg.GetRequestBodyLimit(), cfg.GetResponseBodyLimit()),
	}

	if include := cfg.GetCaptureInclude(); include != "" {
		re, err := regexp.Compile(include)
		if err != nil {
			return nil, fmt.Errorf("invalid --capture-include regex %q: %w", include, err)
		}
		s.includeRE = re
	}
	if exclude := cfg.GetCaptureExclude(); exclude != "" {
		re, err := regexp.Compile(exclude)
		if err != nil {
			return nil, fmt.Errorf("invalid --capture-exclude regex %q: %w", exclude, err)
		}
		s.excludeRE = re
	}

	s.proxy.Verbose = cfg.IsVerbose()

	goproxy.GoproxyCa = ca.TLSCertificate()

	s.proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	s.proxy.OnRequest().DoFunc(s.handleRequest)

	s.proxy.OnResponse().DoFunc(s.handleResponse)

	return s, nil
}

func (s *Server) AddOutput(w output.Writer) {
	s.outputs = append(s.outputs, w)
}

func (s *Server) Run() error {
	addr := fmt.Sprintf(":%d", s.config.ProxyPort())
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	return s.RunWithListener(ln)
}

func (s *Server) RunWithListener(l net.Listener) error {
	if s.config.IsTransparent() {
		return s.runTransparent(l)
	}
	slog.Info("proxy listening", "addr", l.Addr())
	return serveWithTimeouts(s.proxy, l)
}

// serveWithTimeouts serves handler on l with sane connection timeouts. A write
// timeout is intentionally omitted so long-running streams and large
// downloads are never killed mid-transfer; ReadHeaderTimeout alone mitigates
// slow-loris style connection exhaustion.
func serveWithTimeouts(handler http.Handler, l net.Listener) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return srv.Serve(l)
}

func (s *Server) Store() capture.Store {
	return s.store
}

func (s *Server) CA() CAProvider {
	return s.ca
}

func (s *Server) handleRequest(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	// Skip capture entirely for URLs that don't match the configured filters.
	// handleResponse no-ops when UserData is absent, so filtered traffic is
	// proxied but neither stored nor written to outputs.
	if !s.shouldCapture(r) {
		return r, nil
	}

	start := time.Now()
	entry := s.recorder.CaptureRequest(r)
	ctx.UserData = captureCtx{
		entry: entry,
		start: start,
	}
	return r, nil
}

// shouldCapture applies the optional --capture-include / --capture-exclude
// regexes against the full request URL. With no filters set, everything is
// captured (the historical behavior).
func (s *Server) shouldCapture(r *http.Request) bool {
	if s.includeRE != nil && !s.includeRE.MatchString(r.URL.String()) {
		return false
	}
	if s.excludeRE != nil && s.excludeRE.MatchString(r.URL.String()) {
		return false
	}
	return true
}

func (s *Server) handleResponse(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
	data, ok := ctx.UserData.(captureCtx)
	if !ok {
		return resp
	}

	if resp == nil {
		return resp
	}

	bodyBytes, truncated, err := readBodyBounded(resp, s.config.GetResponseBodyLimit())
	if err != nil {
		return resp
	}
	if truncated {
		// The body exceeded the capture limit; the length of the streamed
		// remainder is unknown, so fall back to chunked/close-delimited
		// framing rather than a wrong Content-Length.
		resp.ContentLength = -1
	} else {
		resp.ContentLength = int64(len(bodyBytes))
	}

	result := s.recorder.CaptureResponse(
		resp.StatusCode,
		resp.Header,
		bodyBytes,
		data.start,
	)

	fullEntry := s.recorder.CombineEntry(data.entry, result)
	s.store.Push(fullEntry)

	for _, w := range s.outputs {
		if err := w.WriteEntry(fullEntry); err != nil {
			slog.Error("output write failed", "error", err)
		}
	}

	slog.Debug("request completed",
		"id", fullEntry.ID,
		"method", fullEntry.Method,
		"url", fullEntry.URL,
		"status", fullEntry.StatusCode,
		"duration_ms", fullEntry.DurationMs,
	)

	return resp
}

type captureCtx struct {
	entry capture.CapturedEntry
	start time.Time
}

// readBodyBounded reads at most limit+1 bytes of the response body for
// capture (limit <= 0 reads everything), then rewires resp.Body so the full
// body still streams to the client: the captured prefix is replayed followed
// by whatever remains in the original body. It returns truncated=true when the
// body exceeded the capture limit (the recorder then drops the oversized body).
func readBodyBounded(resp *http.Response, limit int64) ([]byte, bool, error) {
	if resp.Body == nil {
		return nil, false, nil
	}

	var r io.Reader = resp.Body
	if limit > 0 {
		r = io.LimitReader(resp.Body, limit+1)
	}
	buffered, err := io.ReadAll(r)
	if err != nil {
		return nil, false, err
	}

	truncated := limit > 0 && int64(len(buffered)) == limit+1
	resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buffered), resp.Body))
	return buffered, truncated, nil
}
