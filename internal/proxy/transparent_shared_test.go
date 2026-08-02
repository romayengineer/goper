package proxy

import (
	"bufio"
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildSNIExtension returns a full SNI extension (type + length + data).
func buildSNIExtension(sni string) []byte {
	name := []byte(sni)
	var data bytes.Buffer
	data.Write([]byte{0x00, byte(3 + len(name))})             // #nosec G115 -- test SNI is short; length fits in a byte
	data.WriteByte(0x00)                                      // name type: host_name
	data.Write([]byte{byte(len(name) >> 8), byte(len(name))}) // #nosec G115 -- test SNI is short; length fits in a byte
	data.Write(name)

	var ext bytes.Buffer
	ext.Write([]byte{0x00, 0x00})             // extension type: server_name (0)
	ext.Write([]byte{0x00, byte(data.Len())}) // #nosec G115 -- test extension is short; length fits in a byte
	ext.Write(data.Bytes())
	return ext.Bytes()
}

// buildClientHelloRecord builds a TLS record (0x16) wrapping a ClientHello
// handshake with the given SNI extension (or none if sni is "").
func buildClientHelloRecord(sni string) []byte {
	ext := buildSNIExtension(sni)

	var extensions bytes.Buffer
	extensions.Write([]byte{byte(len(ext) >> 8), byte(len(ext))}) // #nosec G115 -- test extension is short; length fits in a byte
	extensions.Write(ext)

	var body bytes.Buffer
	body.Write([]byte{0x03, 0x03}) // client version TLS 1.2
	body.Write(make([]byte, 32))   // random
	body.WriteByte(0)              // session id length
	body.Write([]byte{0x00, 0x02, 0x13, 0x01})
	body.Write([]byte{0x01, 0x00}) // compression methods
	body.Write(extensions.Bytes())

	var handshake bytes.Buffer
	handshake.WriteByte(0x01) // handshake type: ClientHello
	l := body.Len()
	handshake.Write([]byte{byte(l >> 16), byte(l >> 8), byte(l)}) // #nosec G115 -- test handshake is short; length fits in bytes
	handshake.Write(body.Bytes())

	var record bytes.Buffer
	record.WriteByte(0x16) // content type: handshake
	record.Write([]byte{0x03, 0x01})
	hl := handshake.Len()
	record.Write([]byte{byte(hl >> 8), byte(hl)}) // #nosec G115 -- test record is short; length fits in a byte
	record.Write(handshake.Bytes())

	return record.Bytes()
}

func TestDefaultSNIPeeker(t *testing.T) {
	rec := buildClientHelloRecord("api.example.com")
	peeker := DefaultSNIPeeker{}

	hello, err := peeker.Peek(bufio.NewReader(bytes.NewReader(rec)))
	require.NoError(t, err)
	assert.Equal(t, "api.example.com", hello.ServerName)
}

func TestDefaultSNIPeekerNoSNI(t *testing.T) {
	rec := buildClientHelloRecord("")
	peeker := DefaultSNIPeeker{}

	hello, err := peeker.Peek(bufio.NewReader(bytes.NewReader(rec)))
	require.NoError(t, err, "missing SNI should not be an error")
	assert.Empty(t, hello.ServerName, "caller falls back to original destination")
}

func TestDefaultSNIPeekerNotTLS(t *testing.T) {
	peeker := DefaultSNIPeeker{}

	_, err := peeker.Peek(bufio.NewReader(bytes.NewReader([]byte("GET / HTTP/1.1\r\n"))))
	require.Error(t, err)
}

func TestParseClientHelloTruncated(t *testing.T) {
	rec := buildClientHelloRecord("api.example.com")
	assert.Nil(t, parseClientHello(rec[5:len(rec)-3]), "truncated body should yield nil")
}
