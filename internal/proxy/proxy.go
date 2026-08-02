package proxy

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
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

	if err := configureCaptureRegexes(s, cfg); err != nil {
		return nil, err
	}

	s.proxy.Verbose = cfg.IsVerbose()

	goproxy.GoproxyCa = ca.TLSCertificate()

	s.proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	s.proxy.OnRequest().DoFunc(s.handleRequest)

	s.proxy.OnResponse().DoFunc(s.handleResponse)

	return s, nil
}

// configureCaptureRegexes compiles the capture include/exclude filters from the
// config onto the server.
func configureCaptureRegexes(s *Server, cfg config.Provider) error {
	include, err := compileCaptureRegex("--capture-include", cfg.GetCaptureInclude())
	if err != nil {
		return err
	}
	exclude, err := compileCaptureRegex("--capture-exclude", cfg.GetCaptureExclude())
	if err != nil {
		return err
	}
	s.includeRE = include
	s.excludeRE = exclude
	return nil
}

// compileCaptureRegex compiles an optional capture filter regex, returning nil
// for an empty pattern.
func compileCaptureRegex(name, pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid %s regex %q: %w", name, pattern, err)
	}
	return re, nil
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

	bodyBytes, truncated, skipped, err := readBodyBounded(resp, s.config.GetResponseBodyLimit())
	if err != nil {
		return resp
	}
	if skipped {
		// Streaming response (e.g. SSE): the body is left untouched so it
		// streams to the client immediately. ContentLength is preserved (still
		// negative for chunked/close-delimited framing) and the entry is
		// captured without a body.
		s.captureStreaming(data, resp)
		return resp
	}

	fixContentLength(resp, bodyBytes, truncated)
	s.captureResponse(data, resp, bodyBytes)

	return resp
}

// fixContentLength rewrites ContentLength after capture: a truncated body has
// an unknown streamed remainder (chunked/close-delimited framing), a captured
// body gets its exact captured length.
func fixContentLength(resp *http.Response, bodyBytes []byte, truncated bool) {
	if truncated {
		resp.ContentLength = -1
		return
	}
	resp.ContentLength = int64(len(bodyBytes))
}

// captureStreaming records a response whose body was left untouched because it
// is streaming (SSE etc.), storing the entry without a body.
func (s *Server) captureStreaming(data captureCtx, resp *http.Response) {
	result := s.recorder.CaptureResponse(resp.StatusCode, resp.Header, nil, data.start)
	fullEntry := s.recorder.CombineEntry(data.entry, result)
	s.store.Push(fullEntry)
	slog.Debug("request completed (streaming body, not captured)",
		"id", fullEntry.ID,
		"method", fullEntry.Method,
		"url", fullEntry.URL,
		"status", fullEntry.StatusCode,
	)
}

// captureResponse records a completed response into the store and fans it out
// to the configured outputs.
func (s *Server) captureResponse(data captureCtx, resp *http.Response, bodyBytes []byte) {
	result := s.recorder.CaptureResponse(resp.StatusCode, resp.Header, bodyBytes, data.start)
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
}

type captureCtx struct {
	entry capture.CapturedEntry
	start time.Time
}

// readBodyBounded reads the response body for capture, bounded to at most
// limit+1 bytes (limit <= 0 means unlimited). It then rewires resp.Body so the
// full body still streams to the client: the captured prefix is replayed
// followed by whatever remains in the original body.
//
// It returns:
//   - body: the captured prefix (never more than limit+1 bytes)
//   - truncated: true when the body exceeded the capture limit (the recorder
//     then drops the oversized body)
//   - skipped: true when the body was left untouched because it is a streaming
//     response (e.g. SSE) that must not be buffered — proxying would otherwise
//     stall until the limit filled or the stream ended
//   - err: a read failure; even on error resp.Body is rewired with whatever was
//     consumed so the client never loses bytes
func readBodyBounded(resp *http.Response, limit int64) (body []byte, truncated, skipped bool, err error) {
	if resp.Body == nil {
		return nil, false, false, nil
	}

	if isStreamingResponse(resp) {
		return nil, false, true, nil
	}

	buffered, err := readForCapture(resp.Body, resp.ContentLength, limit)
	// Always rewire before returning: on error the consumed prefix must still
	// be delivered to the client followed by the untouched remainder.
	resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buffered), resp.Body))
	if err != nil {
		return nil, false, false, err
	}

	return buffered, captureExceededLimit(resp, buffered, limit), false, nil
}

// captureExceededLimit reports whether the captured prefix hit the capture
// limit, meaning the body was cut off. Unlimited capture still caps the buffer
// at unlimitedReadSafetyCap for chunked/close-delimited bodies.
func captureExceededLimit(resp *http.Response, buffered []byte, limit int64) bool {
	if limit > 0 {
		return int64(len(buffered)) == limit+1
	}
	return resp.ContentLength < 0 && int64(len(buffered)) == unlimitedReadSafetyCap
}

// readForCapture reads a body for capture: at most limit+1 bytes when a limit
// is configured (byte-bounded, so it always terminates), and for unlimited
// mode (limit <= 0) the full body when its length is known, else up to
// unlimitedReadSafetyCap so an endless stream cannot exhaust memory.
func readForCapture(body io.Reader, contentLength, limit int64) ([]byte, error) {
	if limit > 0 {
		return io.ReadAll(io.LimitReader(body, limit+1))
	}
	if contentLength >= 0 {
		return io.ReadAll(body)
	}
	return io.ReadAll(io.LimitReader(body, unlimitedReadSafetyCap))
}

// unlimitedReadSafetyCap bounds how much of an unknown-length (chunked) body
// is buffered when capture is unlimited (limit <= 0). Known-length bodies are
// still captured in full; this only guards against endless streams. A var
// (not const) so tests can shrink it.
var unlimitedReadSafetyCap int64 = 64 << 20 // 64 MiB

// isStreamingResponse reports whether a response carries a body that is
// intended to be consumed incrementally over time (SSE, MJPEG). Buffering such
// a body for capture would stall the client until the capture limit fills or
// the stream ends, so these responses are proxied without body capture.
func isStreamingResponse(resp *http.Response) bool {
	return resp.ContentLength < 0 && isStreamingContentType(resp.Header.Get("Content-Type"))
}

// isStreamingContentType reports whether a Content-Type denotes a
// server-streaming body. Parameters such as "; charset=utf-8" are ignored.
func isStreamingContentType(contentType string) bool {
	ct := strings.ToLower(contentType)
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(ct)
	return ct == "text/event-stream" || ct == "multipart/x-mixed-replace"
}
