package ipsec

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestBuildSocks5TargetRequestKeepsDomain(t *testing.T) {
	req, err := buildSocks5TargetRequest(socks5CmdConnect, "xcap.ims.mnc015.mcc234.pub.3gppnetwork.org", 443)
	if err != nil {
		t.Fatalf("buildSocks5TargetRequest: %v", err)
	}
	host := "xcap.ims.mnc015.mcc234.pub.3gppnetwork.org"
	want := []byte{socks5Version, socks5CmdConnect, 0, socks5ATYPDomain, byte(len(host))}
	want = append(want, host...)
	want = append(want, 0x01, 0xbb)
	if !bytes.Equal(req, want) {
		t.Fatalf("CONNECT domain request = %x, want %x", req, want)
	}
}

func TestDialSOCKS5ConnectsByDomain(t *testing.T) {
	backend := newEchoTCPServer(t)
	defer backend.Close()
	proxy := newTestSOCKS5ConnectProxy(t, backend.Addr().String())
	defer proxy.close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := DialSOCKS5(ctx, Socks5Config{ProxyAddr: proxy.address()}, "tcp", "xcap.example:443")
	if err != nil {
		t.Fatalf("DialSOCKS5: %v", err)
	}
	defer conn.Close()
	if proxy.seenHost != "xcap.example" || proxy.seenPort != 443 {
		t.Fatalf("proxy target = %s:%d", proxy.seenHost, proxy.seenPort)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got) != "ping" {
		t.Fatalf("echo = %q", got)
	}
}

type testSOCKS5ConnectProxy struct {
	t        *testing.T
	tcp      net.Listener
	backend  string
	seenHost string
	seenPort int
}

func newTestSOCKS5ConnectProxy(t *testing.T, backend string) *testSOCKS5ConnectProxy {
	t.Helper()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	proxy := &testSOCKS5ConnectProxy{t: t, tcp: tcp, backend: backend}
	go proxy.serve()
	return proxy
}

func (p *testSOCKS5ConnectProxy) address() string { return p.tcp.Addr().String() }

func (p *testSOCKS5ConnectProxy) close() { _ = p.tcp.Close() }

func (p *testSOCKS5ConnectProxy) serve() {
	conn, err := p.tcp.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	if err := acceptNoAuthHandshake(conn); err != nil {
		p.t.Errorf("handshake: %v", err)
		return
	}
	host, port, err := readConnectTarget(conn)
	if err != nil {
		p.t.Errorf("CONNECT: %v", err)
		return
	}
	p.seenHost, p.seenPort = host, port
	backend, err := net.Dial("tcp", p.backend)
	if err != nil {
		p.t.Errorf("dial backend: %v", err)
		return
	}
	defer backend.Close()
	reply := []byte{socks5Version, 0, 0, socks5ATYPIPv4, 127, 0, 0, 1, 0, 0}
	if _, err := conn.Write(reply); err != nil {
		return
	}
	go func() { _, _ = io.Copy(backend, conn) }()
	_, _ = io.Copy(conn, backend)
}

func readConnectTarget(conn net.Conn) (string, int, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", 0, err
	}
	if header[0] != socks5Version || header[1] != socks5CmdConnect {
		return "", 0, io.ErrUnexpectedEOF
	}
	var host string
	switch header[3] {
	case socks5ATYPDomain:
		ln := make([]byte, 1)
		if _, err := io.ReadFull(conn, ln); err != nil {
			return "", 0, err
		}
		name := make([]byte, int(ln[0]))
		if _, err := io.ReadFull(conn, name); err != nil {
			return "", 0, err
		}
		host = string(name)
	case socks5ATYPIPv4:
		addr := make([]byte, 4)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", 0, err
		}
		host = net.IP(addr).String()
	default:
		return "", 0, io.ErrUnexpectedEOF
	}
	portB := make([]byte, 2)
	if _, err := io.ReadFull(conn, portB); err != nil {
		return "", 0, err
	}
	return host, int(binary.BigEndian.Uint16(portB)), nil
}

func newEchoTCPServer(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo: %v", err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()
	return ln
}
