//go:build !linux && !windows

package ipsec

import (
	"errors"
	"net"
	"syscall"
)

// setUDPEncap is not supported outside Linux (UDP_ENCAP is Linux-only).
func setUDPEncap(conn *net.UDPConn, enable bool) error {
	return errors.New("UDP encapsulation is not supported on this platform")
}

// startErrorListener is a no-op outside Linux (IP_RECVERR/MSG_ERRQUEUE are
// Linux-specific).
func (r *SocketManager) startErrorListener() {
	defer r.wg.Done()
}

// SockExtendedErr mirrors syscall.SockExtendedErr for API compatibility.
type SockExtendedErr struct {
	Errno  uint32
	Origin uint8
	Type   uint8
	Code   uint8
	Info   uint32
	Data   uint32
}

// ParseSockExtError is not supported outside Linux.
func ParseSockExtError(b []byte) (*SockExtendedErr, error) {
	return nil, errors.New("extended socket errors are not supported on this platform")
}

// soReusePort is SO_REUSEPORT on non-Linux Unix platforms.
const soReusePort = syscall.SO_REUSEPORT

// setSocketReuseOptions 设置 SO_REUSEADDR/SO_REUSEPORT（平台相关实现）。
func setSocketReuseOptions(fd uintptr) error {
	if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		return err
	}
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soReusePort, 1)
}

// setSockBindToDevice is a no-op on non-Linux platforms (SO_BINDTODEVICE is Linux-only).
func setSockBindToDevice(fd int, device string) error {
	return errors.New("SO_BINDTODEVICE is not supported on this platform")
}
