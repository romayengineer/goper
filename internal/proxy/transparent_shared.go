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
	header, err := r.Peek(5)
	if err != nil {
		return nil, fmt.Errorf("peek TLS record header: %w", err)
	}
	if header[0] != 0x16 {
		return nil, fmt.Errorf("not a TLS handshake record (0x%02x)", header[0])
	}

	recordLen := int(header[3])<<8 | int(header[4])
	if recordLen <= 0 {
		return nil, fmt.Errorf("invalid TLS record length %d", recordLen)
	}

	total := 5 + recordLen
	data, err := r.Peek(total)
	if err != nil {
		return nil, fmt.Errorf("peek TLS record: %w", err)
	}

	hello := parseClientHello(data[5:total])
	if hello == nil {
		// No SNI extension (or not a ClientHello we recognize): the caller
		// falls back to the original destination, so return an empty hello.
		return &ClientHello{}, nil
	}
	return hello, nil
}

func parseClientHello(data []byte) *ClientHello {
	if len(data) < 2 {
		return nil
	}

	handshakeType := data[0]
	if handshakeType != 1 {
		return nil
	}

	offset := 4

	if offset+2 > len(data) {
		return nil
	}
	offset += 2

	if offset+32 > len(data) {
		return nil
	}
	offset += 32

	if offset+1 > len(data) {
		return nil
	}
	sessionIDLen := int(data[offset])
	offset += 1 + sessionIDLen

	if offset+2 > len(data) {
		return nil
	}
	cipherSuiteLen := int(data[offset])<<8 | int(data[offset+1])
	offset += 2 + cipherSuiteLen

	if offset+1 > len(data) {
		return nil
	}
	compressionLen := int(data[offset])
	offset += 1 + compressionLen

	if offset+2 > len(data) {
		return nil
	}
	extensionsLen := int(data[offset])<<8 | int(data[offset+1])
	offset += 2

	end := offset + extensionsLen
	if end > len(data) {
		end = len(data)
	}

	for offset+4 <= end {
		extType := uint16(data[offset])<<8 | uint16(data[offset+1])
		extLen := int(data[offset+2])<<8 | int(data[offset+3])
		offset += 4

		if extType == 0 && extLen > 5 && offset+extLen <= end {
			listLen := int(data[offset])<<8 | int(data[offset+1])
			nameOffset := offset + 2
			nameEnd := nameOffset + listLen
			if nameEnd > offset+extLen {
				nameEnd = offset + extLen
			}

			for nameOffset+3 <= nameEnd {
				nameType := data[nameOffset]
				nameLen := int(data[nameOffset+1])<<8 | int(data[nameOffset+2])
				nameOffset += 3

				if nameType == 0 && nameLen > 0 && nameOffset+nameLen <= nameEnd {
					sni := string(data[nameOffset : nameOffset+nameLen])
					return &ClientHello{ServerName: sni}
				}
				nameOffset += nameLen
			}
		}

		offset += extLen
	}

	return nil
}
