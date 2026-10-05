//go:build windows

package ipsec

import (
	"errors"
	"net"
)

// Windows 桩：收拢代码的 socket 管理器面向 Linux/Unix；
// Windows 下仅保证编译通过，运行时返回“不支持”。

// setSocketReuseOptions 在 Windows 下是 no-op。
func setSocketReuseOptions(fd uintptr) error { return nil }

// setUDPEncap is not supported on Windows.
func setUDPEncap(conn *net.UDPConn, enable bool) error {
	return errors.New("UDP encapsulation is not supported on Windows")
}

func (r *SocketManager) startErrorListener() {}

// SockExtendedErr mirrors syscall.SockExtendedErr for API compatibility.
type SockExtendedErr struct {
	Errno  uint32
	Origin uint8
	Type   uint8
	Code   uint8
	Info   uint32
	Data   uint32
}

// ParseSockExtError is not supported on Windows.
func ParseSockExtError(b []byte) (*SockExtendedErr, error) {
	return nil, errors.New("extended socket errors are not supported on Windows")
}

// setSockBindToDevice is not supported on Windows (SO_BINDTODEVICE is Linux-only).
func setSockBindToDevice(fd int, device string) error {
	return errors.New("SO_BINDTODEVICE is not supported on Windows")
}
