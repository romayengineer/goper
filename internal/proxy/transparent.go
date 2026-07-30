package proxy

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

type OriginalDst struct {
	IP   net.IP
	Port int
}

const SO_ORIGINAL_DST = 80

func GetOriginalDst(conn net.Conn) (*OriginalDst, error) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return nil, fmt.Errorf("not a TCP connection")
	}

	file, err := tcpConn.File()
	if err != nil {
		return nil, fmt.Errorf("get file descriptor: %w", err)
	}
	defer file.Close()

	fd := int(file.Fd())

	var addr syscall.RawSockaddrInet4
	addrLen := syscall.SizeofSockaddrInet4

	_, _, errno := syscall.Syscall6(
		syscall.SYS_GETSOCKOPT,
		uintptr(fd),
		uintptr(syscall.IPPROTO_IP),
		SO_ORIGINAL_DST,
		uintptr(unsafe.Pointer(&addr)),
		uintptr(unsafe.Pointer(&addrLen)),
		0,
	)
	if errno != 0 {
		return nil, fmt.Errorf("SO_ORIGINAL_DST: %w", errno)
	}

	ip := net.IP(addr.Addr[:])
	port := int(binary.BigEndian.Uint16((*[2]byte)(unsafe.Pointer(&addr.Port))[:]))

	return &OriginalDst{
		IP:   ip,
		Port: port,
	}, nil
}

type ClientHello struct {
	ServerName string
}

func PeekClientHello(conn net.Conn) (*ClientHello, error) {
	rawConn, err := conn.(*net.TCPConn).SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("get syscall conn: %w", err)
	}

	var hello *ClientHello
	var readErr error

	rawConn.Read(func(fd uintptr) bool {
		var buf [4096]byte
		n, err := syscall.Read(int(fd), buf[:])
		if err != nil {
			readErr = fmt.Errorf("read: %w", err)
			return true
		}
		if n < 5 {
			readErr = fmt.Errorf("too short for TLS record")
			return true
		}

		if buf[0] != 0x16 {
			return true
		}

		tlsLen := int(buf[3])<<8 | int(buf[4])
		if n < 5+tlsLen {
			readErr = fmt.Errorf("incomplete TLS record")
			return true
		}

		hello = parseClientHello(buf[5 : 5+tlsLen])
		return true
	})

	if readErr != nil {
		return nil, readErr
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
