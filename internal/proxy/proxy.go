package proxy

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/romayengineer/goper/internal/capture"
	"github.com/romayengineer/goper/internal/config"
)

type Server struct {
	proxy  *goproxy.ProxyHttpServer
	store  *capture.RingBuffer
	cache  *CertCache
	ca     *CA
	config *config.Config
}

func NewServer(cfg *config.Config) (*Server, error) {
	ca, err := LoadOrCreateCA(cfg.CADir)
	if err != nil {
		return nil, fmt.Errorf("load CA: %w", err)
	}

	store := capture.NewRingBuffer(cfg.BufferSize)
	cache := NewCertCache(ca)

	s := &Server{
		proxy:  goproxy.NewProxyHttpServer(),
		store:  store,
		cache:  cache,
		ca:     ca,
		config: cfg,
	}

	s.proxy.Verbose = cfg.Verbose

	goproxy.GoproxyCa = ca.TLS

	s.proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	s.proxy.OnRequest().DoFunc(s.handleRequest)

	s.proxy.OnResponse().DoFunc(s.handleResponse)

	return s, nil
}

func (s *Server) Run() error {
	addr := fmt.Sprintf(":%d", s.config.Port)
	log.Printf("goper proxy listening on %s", addr)

	if s.config.Transparent {
		log.Println("transparent mode enabled (iptables must redirect traffic)")
	}

	return http.ListenAndServe(addr, s.proxy)
}

func (s *Server) RunWithListener(l net.Listener) error {
	log.Printf("goper proxy listening on %s", l.Addr())
	return http.Serve(l, s.proxy)
}

func (s *Server) Store() *capture.RingBuffer {
	return s.store
}

func (s *Server) CA() *CA {
	return s.ca
}

func (s *Server) handleRequest(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	start := time.Now()
	entry := capture.CaptureRequest(r)
	ctx.UserData = captureCtx{
		entry: entry,
		start: start,
	}
	return r, nil
}

func (s *Server) handleResponse(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
	data, ok := ctx.UserData.(captureCtx)
	if !ok {
		return resp
	}

	if resp == nil {
		return resp
	}

	bodyBytes, err := readBody(resp)
	if err != nil {
		return resp
	}

	result := capture.CaptureResponse(
		resp.StatusCode,
		resp.Header,
		bodyBytes,
		data.start,
	)

	fullEntry := capture.CombineEntry(data.entry, result)
	s.store.Push(fullEntry)

	if s.config.Verbose {
		log.Printf("[%s] %s %s → %d (%dms)", fullEntry.ID, fullEntry.Method, fullEntry.URL, fullEntry.StatusCode, fullEntry.DurationMs)
	}

	return resp
}

type captureCtx struct {
	entry capture.CapturedEntry
	start time.Time
}

func readBody(resp *http.Response) ([]byte, error) {
	if resp.Body == nil {
		return nil, nil
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}
