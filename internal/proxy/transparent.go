//go:build linux

package proxy

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

const SO_ORIGINAL_DST = 80

// GetOriginalDst returns the original destination of a connection that was
// redirected to this proxy by an iptables REDIRECT rule (SO_ORIGINAL_DST).
// Linux-only.
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

// LinuxResolver resolves the original destination via SO_ORIGINAL_DST.
type LinuxResolver struct{}

func (LinuxResolver) Resolve(conn net.Conn) (*OriginalDst, error) {
	return GetOriginalDst(conn)
}
