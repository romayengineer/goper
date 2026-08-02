package proxy

import (
	"bufio"
	"fmt"
	"net"
)

// OriginalDst describes the destination a connection was originally bound to
// before it was redirected to the proxy.
type OriginalDst struct {
	IP   net.IP
	Port int
}

// OriginalDstResolver discovers the original destination of a redirected
// connection (Linux: SO_ORIGINAL_DST). Abstracted so tests can inject fakes.
type OriginalDstResolver interface {
	Resolve(conn net.Conn) (*OriginalDst, error)
}

// ClientHello carries the SNI hostname extracted from a TLS ClientHello.
type ClientHello struct {
	ServerName string
}

// SNIPeeker reads the SNI hostname from a TLS ClientHello without consuming
// the bytes, so the same buffered reader can feed the subsequent TLS
// handshake. The reader must be non-destructively peekable (bufio.Reader).
type SNIPeeker interface {
	Peek(r *bufio.Reader) (*ClientHello, error)
}

// DefaultSNIPeeker extracts SNI using bufio.Reader.Peek (non-destructive).
type DefaultSNIPeeker struct{}

func (DefaultSNIPeeker) Peek(r *bufio.Reader) (*ClientHello, error) {
	data, err := peekTLSRecord(r)
	if err != nil {
		return nil, err
	}

	hello := parseClientHello(data[5:])
	if hello == nil {
		// No SNI extension (or not a ClientHello we recognize): the caller
		// falls back to the original destination, so return an empty hello.
		return &ClientHello{}, nil
	}
	return hello, nil
}

// peekTLSRecord non-destructively reads a complete TLS handshake record.
func peekTLSRecord(r *bufio.Reader) ([]byte, error) {
	header, err := r.Peek(5)
	if err != nil {
		return nil, fmt.Errorf("peek TLS record header: %w", err)
	}

	recordLen, err := tlsRecordHeader(header)
	if err != nil {
		return nil, err
	}

	return peekRecordBody(r, recordLen)
}

// peekRecordBody peeks the full TLS record payload.
func peekRecordBody(r *bufio.Reader, recordLen int) ([]byte, error) {
	data, err := r.Peek(5 + recordLen)
	if err != nil {
		return nil, fmt.Errorf("peek TLS record: %w", err)
	}
	return data, nil
}

// tlsRecordHeader parses a 5-byte TLS record header, returning the record
// length or an error describing the invalid header.
func tlsRecordHeader(header []byte) (int, error) {
	if header[0] != 0x16 {
		return 0, fmt.Errorf("not a TLS handshake record (0x%02x)", header[0])
	}

	recordLen := int(header[3])<<8 | int(header[4])
	if recordLen <= 0 {
		return 0, fmt.Errorf("invalid TLS record length %d", recordLen)
	}
	return recordLen, nil
}

func parseClientHello(data []byte) *ClientHello {
	if !isHandshakeType(data) {
		return nil
	}

	offset, extensionsLen, ok := skipClientHelloFixed(data, 4)
	if !ok {
		return nil
	}

	return helloFromSNI(data, offset, extensionsLen)
}

// isHandshakeType reports whether the data starts a TLS ClientHello handshake.
func isHandshakeType(data []byte) bool {
	return len(data) >= 2 && data[0] == 1
}

// helloFromSNI scans the extensions block for an SNI hostname, returning a
// ClientHello carrying it, or nil when none is present.
func helloFromSNI(data []byte, offset, extensionsLen int) *ClientHello {
	end := offset + extensionsLen
	if end > len(data) {
		end = len(data)
	}

	if sni := sniFromExtensions(data, offset, end); sni != "" {
		return &ClientHello{ServerName: sni}
	}
	return nil
}

// skipClientHelloFixed walks the fixed ClientHello fields (version, random,
// session id, cipher suites, compression) with bounds checks, returning the
// offset of the extensions block and its length, or ok=false if the data is
// truncated.
func skipClientHelloFixed(data []byte, offset int) (extOffset, extLen int, ok bool) {
	offset, ok = skipClientHelloBody(data, offset)
	if !ok {
		return 0, 0, false
	}

	if offset+2 > len(data) {
		return 0, 0, false
	}
	extensionsLen := int(data[offset])<<8 | int(data[offset+1])

	return offset + 2, extensionsLen, true
}

// skipClientHelloBody skips the fixed version/random and the variable session,
// cipher suites and compression fields.
func skipClientHelloBody(data []byte, offset int) (int, bool) {
	offset, ok := skipClientHelloVersionRandom(data, offset)
	if !ok {
		return 0, false
	}
	return skipClientHelloVariable(data, offset)
}

// skipClientHelloVersionRandom skips the TLS version and random fields.
func skipClientHelloVersionRandom(data []byte, offset int) (int, bool) {
	if offset+34 > len(data) {
		return 0, false
	}
	return offset + 34, true
}

// skipClientHelloVariable consumes the variable-length session id, cipher
// suites and compression fields, returning the offset after them, or ok=false
// if the data is truncated.
func skipClientHelloVariable(data []byte, offset int) (int, bool) {
	offset, ok := skipSessionAndCipher(data, offset)
	if !ok {
		return 0, false
	}

	if offset+1 > len(data) {
		return 0, false
	}
	compressionLen := int(data[offset])
	offset += 1 + compressionLen

	return offset, true
}

// skipSessionAndCipher skips the session id and cipher suites fields.
func skipSessionAndCipher(data []byte, offset int) (int, bool) {
	if offset+1 > len(data) {
		return 0, false
	}
	sessionIDLen := int(data[offset])
	offset += 1 + sessionIDLen

	if offset+2 > len(data) {
		return 0, false
	}
	cipherSuiteLen := int(data[offset])<<8 | int(data[offset+1])
	offset += 2 + cipherSuiteLen

	return offset, true
}

// sniFromExtensions walks the TLS extensions block (data[offset:end]) looking
// for the SNI (server_name, type 0) extension and returns its first hostname,
// or "" if none is present.
func sniFromExtensions(data []byte, offset, end int) string {
	for offset+4 <= end {
		extType := uint16(data[offset])<<8 | uint16(data[offset+1])
		extLen := int(data[offset+2])<<8 | int(data[offset+3])
		if name := sniFromExtension(data, offset+4, end, extType, extLen); name != "" {
			return name
		}
		offset += 4 + extLen
	}

	return ""
}

// sniFromExtension extracts the SNI hostname from one extension, if present.
func sniFromExtension(data []byte, offset, end int, extType uint16, extLen int) string {
	if !isSNIExtension(extType, extLen, offset, end) {
		return ""
	}
	return sniFromServerNameList(data, offset, end, extLen)
}

// isSNIExtension reports whether the extension at offset is an SNI
// (server_name, type 0) extension whose ServerNameList fits within the block.
func isSNIExtension(extType uint16, extLen, offset, end int) bool {
	return extType == 0 && extLen > 5 && offset+extLen <= end
}

// sniFromServerNameList walks the ServerNameList inside an SNI extension
// (starting at data[offset], bounded by end and the extension length) and
// returns the first DNS hostname entry, or "" if none is present.
func sniFromServerNameList(data []byte, offset, end, extLen int) string {
	listLen := int(data[offset])<<8 | int(data[offset+1])
	nameOffset := offset + 2
	nameEnd := clampServerNameListEnd(nameOffset+listLen, offset, extLen)

	for nameOffset+3 <= nameEnd {
		nameType := data[nameOffset]
		nameLen := int(data[nameOffset+1])<<8 | int(data[nameOffset+2])
		nameOffset += 3

		if isDNSEntry(nameType, nameLen, nameOffset, nameEnd) {
			return string(data[nameOffset : nameOffset+nameLen])
		}
		nameOffset += nameLen
	}

	return ""
}

// clampServerNameListEnd bounds the ServerNameList end to the extension length.
func clampServerNameListEnd(nameEnd, extStart, extLen int) int {
	if nameEnd > extStart+extLen {
		return extStart + extLen
	}
	return nameEnd
}

// isDNSEntry reports whether a ServerNameList entry is a DNS name that fits
// within the remaining list.
func isDNSEntry(nameType byte, nameLen, nameOffset, nameEnd int) bool {
	return nameType == 0 && nameLen > 0 && nameOffset+nameLen <= nameEnd
}
