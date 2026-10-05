package ipsec

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// DialSOCKS5 opens a TCP connection through a SOCKS5 CONNECT. The target host
// is sent as a domain name so the proxy, not the local LAN resolver, does DNS.
func DialSOCKS5(ctx context.Context, cfg Socks5Config, network, address string) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	network = strings.ToLower(strings.TrimSpace(network))
	if network == "" {
		network = "tcp"
	}
	if !strings.HasPrefix(network, "tcp") {
		return nil, fmt.Errorf("SOCKS5 CONNECT supports tcp, not %q", network)
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid SOCKS5 port %q", portText)
	}
	timeout := defaultSocks5Timeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, ctx.Err()
		}
		timeout = remaining
	}
	conn, err := connectSocks5(cfg, timeout)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(timeout))
	}
	if err := socks5Handshake(conn, &cfg); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("SOCKS5 handshake: %w", err)
	}
	if err := socks5Connect(conn, host, uint16(port)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}
