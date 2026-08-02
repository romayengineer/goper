package proxy

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/romayengineer/goper/internal/capture"
	"github.com/romayengineer/goper/internal/config"
	"github.com/romayengineer/goper/internal/httpx"
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
	ln, err := httpx.Listen(s.config.ProxyPort())
	if err != nil {
		return err
	}
	return s.RunWithListener(ln)
}

func (s *Server) RunWithListener(l net.Listener) error {
	if s.config.IsTransparent() {
		return s.runTransparent(l)
	}
	slog.Info("proxy listening", "addr", l.Addr())
	return httpx.Serve(s.proxy, l, 0)
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
	url := r.URL.String()
	if !s.regexAllows(s.includeRE, url) {
		return false
	}
	if s.regexExcludes(s.excludeRE, url) {
		return false
	}
	return true
}

// regexAllows reports whether url passes an include filter (nil = allow all).
func (s *Server) regexAllows(re *regexp.Regexp, url string) bool {
	return re == nil || re.MatchString(url)
}

// regexExcludes reports whether url matches an exclude filter (nil = none).
func (s *Server) regexExcludes(re *regexp.Regexp, url string) bool {
	return re != nil && re.MatchString(url)
}

func (s *Server) handleResponse(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
	data, ok := s.responseData(ctx, resp)
	if !ok {
		return resp
	}

	s.recordResponse(data, resp)

	return resp
}

// recordResponse captures a completed response into the store, skipping
// streaming bodies (e.g. SSE) that must stream to the client untouched.
func (s *Server) recordResponse(data captureCtx, resp *http.Response) {
	bodyBytes, truncated, skipped, err := readBodyBounded(resp, s.config.GetResponseBodyLimit())
	if err != nil {
		return
	}
	if skipped {
		// Streaming response (e.g. SSE): the body is left untouched so it
		// streams to the client immediately. ContentLength is preserved (still
		// negative for chunked/close-delimited framing) and the entry is
		// captured without a body.
		s.captureStreaming(data, resp)
		return
	}

	fixContentLength(resp, bodyBytes, truncated)
	s.captureResponse(data, resp, bodyBytes)
}

// responseData extracts the capture context, skipping nil responses.
func (s *Server) responseData(ctx *goproxy.ProxyCtx, resp *http.Response) (captureCtx, bool) {
	data, ok := ctx.UserData.(captureCtx)
	if !ok || resp == nil {
		return captureCtx{}, false
	}
	return data, true
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
