package proxy

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/romayengineer/goper/internal/httpx"
)

// peekTimeout bounds how long we wait for the first bytes of a connection
// before giving up.
const peekTimeout = 10 * time.Second

// errTransparentUnsupported is returned when transparent mode is requested on
// a platform without an original-destination resolver (non-Linux).
var errTransparentUnsupported = fmt.Errorf("transparent mode requires a platform original-destination resolver (linux)")

// bufferedConn is a net.Conn whose reads first drain a shared bufio.Reader,
// preserving any bytes already buffered (e.g. a peeked ClientHello).
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

// singleConnListener returns a single accepted connection, then blocks until
// Close is called so http.Server.Serve stays alive while the inner connection
// is in use. It must be closed when the inner connection is done; otherwise
// the accept loop would hang forever.
type singleConnListener struct {
	conn   net.Conn
	addr   net.Addr
	mu     sync.Mutex
	served bool
	closed chan struct{}
	once   sync.Once
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if !l.served {
		l.served = true
		conn := l.conn
		l.mu.Unlock()
		return conn, nil
	}
	l.mu.Unlock()
	<-l.closed
	return nil, net.ErrClosed
}

func (l *singleConnListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *singleConnListener) Addr() net.Addr { return l.addr }

// closeNotifyConn calls onClose when the underlying connection is closed,
// used to release the singleConnListener when http.Server finishes the conn.
type closeNotifyConn struct {
	net.Conn
	onClose func()
	once    sync.Once
}

func (c *closeNotifyConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.onClose)
	return err
}

// runTransparent accepts connections on l and transparently proxies them,
// peeking each to decide between plain HTTP and TLS MITM.
func (s *Server) runTransparent(l net.Listener) error {
	if !s.initTransparent() {
		return errTransparentUnsupported
	}

	slog.Info("transparent proxy running",
		"addr", l.Addr(),
		"resolver", fmt.Sprintf("%T", s.resolver),
	)

	return s.acceptLoop(l)
}

// acceptLoop accepts and handles connections until a fatal error.
func (s *Server) acceptLoop(l net.Listener) error {
	for {
		conn, err := acceptOnce(l)
		if err != nil {
			return err
		}
		go s.handleTransparentConn(conn)
	}
}

// acceptOnce accepts one connection, retrying transient timeouts until a
// connection or a fatal error arrives.
func acceptOnce(l net.Listener) (net.Conn, error) {
	for {
		conn, err := l.Accept()
		if acceptContinue(err) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		return conn, err
	}
}

// acceptContinue reports whether a transient accept error should be retried.
func acceptContinue(err error) bool {
	return err != nil && acceptRetryable(err)
}

// initTransparent defaults the resolver and peeker if unset. It reports false
// when no SNI resolver is available, meaning transparent mode is unsupported.
func (s *Server) initTransparent() bool {
	if !s.initResolver() {
		return false
	}
	if s.peeker == nil {
		s.peeker = DefaultSNIPeeker{}
	}
	return true
}

// initResolver defaults the resolver if unset, reporting whether one exists.
func (s *Server) initResolver() bool {
	if s.resolver == nil {
		s.resolver = defaultResolver()
	}
	return s.resolver != nil
}

// acceptRetryable reports whether a listener error is transient (a timeout),
// in which case the accept loop should retry instead of failing.
func acceptRetryable(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func (s *Server) handleTransparentConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	// Ensure defaults even if invoked outside runTransparent (mirrors the
	// initialization runTransparent performs for its accept loop).
	if !s.initTransparent() {
		return
	}

	first, br, ok := peekAndBuffer(conn)
	if !ok {
		return
	}

	s.dispatchTransparent(first, br, conn)
}

// peekAndBuffer reads the first byte to classify the connection, returning a
// buffered reader over the same connection.
func peekAndBuffer(conn net.Conn) (first byte, br *bufio.Reader, ok bool) {
	if !setPeekDeadline(conn) {
		return 0, nil, false
	}
	br = bufio.NewReaderSize(conn, 64<<10)

	firstBytes, err := br.Peek(1)
	if err != nil {
		return 0, nil, false
	}

	// Clear the peek deadline; the proxied session is long-lived.
	_ = conn.SetReadDeadline(time.Time{})

	return firstBytes[0], br, true
}

// setPeekDeadline bounds the SNI peek window, reporting success.
func setPeekDeadline(conn net.Conn) bool {
	if err := conn.SetReadDeadline(time.Now().Add(peekTimeout)); err != nil {
		return false
	}
	return true
}

// dispatchTransparent routes a transparent connection to TLS or HTTP handling
// based on the first byte (0x16 = TLS handshake record).
func (s *Server) dispatchTransparent(first byte, br *bufio.Reader, conn net.Conn) {
	if first == 0x16 {
		s.handleTLSTransparent(br, conn)
		return
	}
	s.handleHTTPTransparent(br, conn)
}

func (s *Server) handleHTTPTransparent(br *bufio.Reader, conn net.Conn) {
	orig, _ := s.resolver.Resolve(conn)
	fallbackHost := ""
	if orig != nil {
		fallbackHost = net.JoinHostPort(orig.IP.String(), strconv.Itoa(orig.Port))
	}

	s.serveInner(&bufferedConn{Conn: conn, r: br}, "http", fallbackHost)
}

func (s *Server) handleTLSTransparent(br *bufio.Reader, conn net.Conn) {
	hello, err := s.peeker.Peek(br)
	if err != nil {
		slog.Debug("transparent: SNI peek failed", "error", err)
		return
	}

	fallbackHost := s.fallbackHost(conn, hello.ServerName)

	tlsConn := tls.Server(&bufferedConn{Conn: conn, r: br}, s.tlsConfigFor(fallbackHost))
	if err := tlsConn.Handshake(); err != nil {
		slog.Debug("transparent: TLS handshake failed", "error", err)
		return
	}

	s.serveInner(tlsConn, "https", fallbackHost)
}

// fallbackHost resolves the original destination for a transparent connection,
// preferring the SNI hostname when the client supplied one.
func (s *Server) fallbackHost(conn net.Conn, serverName string) string {
	if serverName != "" {
		return serverName
	}
	orig, _ := s.resolver.Resolve(conn)
	if orig != nil {
		return orig.IP.String()
	}
	return ""
}

// tlsConfigFor builds the MITM TLS config for a transparent connection,
// falling back to the original destination when the client omits SNI.
func (s *Server) tlsConfigFor(fallbackHost string) *tls.Config {
	return &tls.Config{
		GetCertificate: func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			host := clientHello.ServerName
			if host == "" {
				host = fallbackHost
			}
			helloCopy := *clientHello
			helloCopy.ServerName = host
			return s.cache.GetCertificate(&helloCopy)
		},
		NextProtos: []string{"http/1.1"},
		MinVersion: tls.VersionTLS12,
	}
}

// serveInner runs the goproxy handler over a single inner connection,
// rewriting relative (transparent-style) request URLs to absolute ones.
func (s *Server) serveInner(inner net.Conn, scheme, fallbackHost string) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rewriteTransparentURL(r, scheme, fallbackHost)
		s.proxy.ServeHTTP(w, r)
	})

	l := &singleConnListener{
		addr:   inner.LocalAddr(),
		closed: make(chan struct{}),
	}
	// Wrap the conn so that when http.Server closes it, the listener is
	// released and http.Serve returns.
	l.conn = &closeNotifyConn{Conn: inner, onClose: func() { _ = l.Close() }}

	_ = httpx.Serve(handler, l, 0)
}

// rewriteTransparentURL makes a relative (transparent-style) request URL
// absolute so the proxy can route it.
func rewriteTransparentURL(r *http.Request, scheme, fallbackHost string) {
	if isAbsolute(r) {
		return
	}
	r.URL.Scheme = scheme
	r.URL.Host = r.Host
	if r.URL.Host == "" {
		r.URL.Host = fallbackHost
	}
}

// isAbsolute reports whether the request targets an absolute URL or is an
// HTTPS CONNECT tunnel.
func isAbsolute(r *http.Request) bool {
	return r.Method == http.MethodConnect || r.URL.IsAbs()
}
