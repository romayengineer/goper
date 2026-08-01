package proxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
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
	if s.resolver == nil {
		s.resolver = defaultResolver()
	}
	if s.resolver == nil {
		return errTransparentUnsupported
	}
	if s.peeker == nil {
		s.peeker = DefaultSNIPeeker{}
	}

	slog.Info("transparent proxy running",
		"addr", l.Addr(),
		"resolver", fmt.Sprintf("%T", s.resolver),
	)

	for {
		conn, err := l.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return err
		}
		go s.handleTransparentConn(conn)
	}
}

func (s *Server) handleTransparentConn(conn net.Conn) {
	defer conn.Close()

	// Ensure defaults even if invoked outside runTransparent (mirrors the
	// initialization runTransparent performs for its accept loop).
	if s.resolver == nil {
		s.resolver = defaultResolver()
	}
	if s.peeker == nil {
		s.peeker = DefaultSNIPeeker{}
	}

	if err := conn.SetReadDeadline(time.Now().Add(peekTimeout)); err != nil {
		return
	}
	br := bufio.NewReaderSize(conn, 64<<10)

	first, err := br.Peek(1)
	if err != nil {
		return
	}

	// Clear the peek deadline; the proxied session is long-lived.
	_ = conn.SetReadDeadline(time.Time{})

	if first[0] == 0x16 {
		s.handleTLSTransparent(br, conn)
	} else {
		s.handleHTTPTransparent(br, conn)
	}
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

	orig, _ := s.resolver.Resolve(conn)
	fallbackHost := ""
	if orig != nil {
		fallbackHost = orig.IP.String()
	}
	if hello.ServerName != "" {
		fallbackHost = hello.ServerName
	}

	tlsCfg := &tls.Config{
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

	tlsConn := tls.Server(&bufferedConn{Conn: conn, r: br}, tlsCfg)
	if err := tlsConn.Handshake(); err != nil {
		slog.Debug("transparent: TLS handshake failed", "error", err)
		return
	}

	s.serveInner(tlsConn, "https", fallbackHost)
}

// serveInner runs the goproxy handler over a single inner connection,
// rewriting relative (transparent-style) request URLs to absolute ones.
func (s *Server) serveInner(inner net.Conn, scheme, fallbackHost string) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect && !r.URL.IsAbs() {
			r.URL.Scheme = scheme
			r.URL.Host = r.Host
			if r.URL.Host == "" {
				r.URL.Host = fallbackHost
			}
		}
		s.proxy.ServeHTTP(w, r)
	})

	l := &singleConnListener{
		addr:   inner.LocalAddr(),
		closed: make(chan struct{}),
	}
	// Wrap the conn so that when http.Server closes it, the listener is
	// released and http.Serve returns.
	l.conn = &closeNotifyConn{Conn: inner, onClose: func() { _ = l.Close() }}

	_ = http.Serve(l, handler)
}
