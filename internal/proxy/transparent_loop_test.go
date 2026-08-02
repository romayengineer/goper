package proxy

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockResolver struct {
	orig *OriginalDst
	err  error
}

func (m mockResolver) Resolve(conn net.Conn) (*OriginalDst, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.orig, nil
}

var _ OriginalDstResolver = mockResolver{}

func TestSingleConnListenerServesConnOnce(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	l := &singleConnListener{conn: server, addr: server.LocalAddr(), closed: make(chan struct{})}

	got, err := l.Accept()
	require.NoError(t, err)
	assert.Same(t, server, got, "first Accept must return the single connection")
}

func TestSingleConnListenerSecondAcceptBlocksUntilClose(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	l := &singleConnListener{conn: server, addr: server.LocalAddr(), closed: make(chan struct{})}

	_, err := l.Accept()
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		done <- err
	}()

	assertBlocks(t, done)

	require.NoError(t, l.Close())

	assertErrorEventually(t, done)
}

// assertBlocks asserts that done does not receive within 100ms.
func assertBlocks(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("second Accept must block until Close")
	case <-time.After(100 * time.Millisecond):
	}
}

// assertErrorEventually asserts that done receives an error within 1s.
func assertErrorEventually(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		require.Error(t, err)
		assert.Equal(t, net.ErrClosed, err)
	case <-time.After(time.Second):
		t.Fatal("second Accept must return once the listener is closed")
	}
}

func TestSingleConnListenerCloseIdempotent(t *testing.T) {
	l := &singleConnListener{closed: make(chan struct{})}

	require.NoError(t, l.Close())
	require.NoError(t, l.Close(), "closing twice must not panic (sync.Once)")
}

func TestSingleConnListenerAddr(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	addr := server.LocalAddr()
	l := &singleConnListener{conn: server, addr: addr, closed: make(chan struct{})}
	assert.Equal(t, addr, l.Addr())
}

func TestBufferedConnReadsBufferedBytesFirst(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	go func() {
		_, _ = client.Write([]byte("hello"))
		_ = client.Close() // EOF so reads after the buffered data don't block
	}()

	br := bufio.NewReader(server)
	_, err := br.Peek(2) // pull "he" into the buffer (bufio over-reads: "hello")
	require.NoError(t, err)

	bc := &bufferedConn{Conn: server, r: br}
	buf := make([]byte, 5)

	n, err := bc.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(buf[:n]), "buffered bytes must be drained first, before the conn")

	n, err = bc.Read(buf)
	assert.Zero(t, n, "no more data after the buffered bytes")
	require.Error(t, err)
	assert.ErrorIs(t, err, io.EOF)
}

func TestCloseNotifyConnCallsOnCloseOnce(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()

	calls := 0
	cn := &closeNotifyConn{Conn: server, onClose: func() { calls++ }}

	require.NoError(t, cn.Close())
	require.NoError(t, cn.Close())
	assert.Equal(t, 1, calls, "onClose must fire exactly once")
}

// TestHandleTransparentConnHTTPOverLoopback drives a plain HTTP request
// through the full transparent path: peek → branch → URL rewrite → goproxy →
// capture. The mock resolver points at a local upstream, so no real network.
func TestHandleTransparentConnHTTPOverLoopback(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"transparent":true}`))
	}))
	defer target.Close()

	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	s := newTestServer(t, testConfig(t))
	s.resolver = mockResolver{orig: &OriginalDst{IP: net.ParseIP(host), Port: port}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		s.handleTransparentConn(conn)
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	targetAddr := strings.TrimPrefix(target.URL, "http://")
	_, err = fmt.Fprintf(client, "GET /hello HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", targetAddr)
	require.NoError(t, err)

	_ = client.SetReadDeadline(time.Now().Add(10 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"transparent":true}`, string(body))

	entry := waitForEntry(t, s.Store())
	require.NotNil(t, entry, "transparent HTTP request must be captured")
	assert.Equal(t, http.MethodGet, entry.Method)
	assert.Equal(t, "http", entry.Scheme)
	assert.Contains(t, entry.URL, targetAddr)
}

// TestHandleTransparentConnTLSPeekFailure covers the TLS branch when the SNI
// peek cannot complete (truncated record): the connection must be dropped
// gracefully, nothing captured, no panic.
func TestHandleTransparentConnTLSPeekFailure(t *testing.T) {
	s := newTestServer(t, testConfig(t))
	s.resolver = mockResolver{}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		s.handleTransparentConn(conn)
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)

	// 0x16 = TLS handshake record, but truncated: the SNI peek cannot finish.
	_, err = client.Write([]byte{0x16, 0x03, 0x01})
	require.NoError(t, err)
	_ = client.Close()

	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.Copy(io.Discard, client) // drain until EOF

	assert.Zero(t, s.Store().Len(), "failed TLS peek must not capture anything")
}

// TestHandleTransparentConnTLSOverLoopback drives an HTTPS request through the
// full transparent TLS path: ClientHello → SNI peek → MITM handshake (goper
// CA) → inner HTTP → goproxy → TLS target → capture.
func TestHandleTransparentConnTLSOverLoopback(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mitm":true}`))
	}))
	defer target.Close()

	targetAddr := strings.TrimPrefix(target.URL, "https://")

	s := newTestServer(t, testConfig(t))
	// goproxy must trust the TLS target's cert when re-forwarding.
	targetPool := x509.NewCertPool()
	targetPool.AddCert(target.Certificate())
	s.proxy.Tr = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: targetPool}}
	s.resolver = mockResolver{}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		s.handleTransparentConn(conn)
	}()

	// The client trusts goper's CA, so the MITM certificate (signed for SNI
	// "localhost") validates.
	clientPool := x509.NewCertPool()
	clientPool.AddCert(s.CA().Certificate())

	raw, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()

	client := tls.Client(raw, &tls.Config{RootCAs: clientPool, ServerName: "localhost"})
	require.NoError(t, client.Handshake())

	_, err = fmt.Fprintf(client, "GET /secure HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", targetAddr)
	require.NoError(t, err)

	_ = client.SetReadDeadline(time.Now().Add(10 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"mitm":true}`, string(body))

	entry := waitForEntry(t, s.Store())
	require.NotNil(t, entry, "transparent HTTPS request must be captured")
	assert.Equal(t, http.MethodGet, entry.Method)
	assert.Equal(t, "https", entry.Scheme)
	require.NotNil(t, entry.ResponseBody)
	assert.JSONEq(t, `{"mitm":true}`, *entry.ResponseBody)
}

// TestRunTransparentAcceptLoop exercises the accept loop: it serves a
// connection, then returns once the listener is closed.
func TestRunTransparentAcceptLoop(t *testing.T) {
	s := newTestServer(t, testConfig(t))
	s.resolver = mockResolver{}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- s.runTransparent(ln) }()

	// A connection that closes immediately: handled in the background, no panic.
	conn, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	_ = conn.Close()

	// Closing the listener unblocks the accept loop.
	require.NoError(t, ln.Close())

	select {
	case err := <-done:
		require.Error(t, err, "runTransparent must return the accept error")
		assert.Contains(t, err.Error(), "closed")
	case <-time.After(2 * time.Second):
		t.Fatal("runTransparent did not return after listener close")
	}
}
