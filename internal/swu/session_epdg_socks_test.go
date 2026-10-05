package swu

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestEPDGCandidatesReachSOCKS5DatagramDestination(t *testing.T) {
	requireLoopbackUDP(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	relay, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relay.Close() })
	served := make(chan error, 2)
	go func() {
		for range 2 {
			conn, err := listener.Accept()
			if err == nil {
				err = serveCandidateSOCKSControl(conn, relay.LocalAddr().(*net.UDPAddr))
				_ = conn.Close()
			}
			served <- err
		}
	}()
	store := NewEPDGCandidateStore(candidateResolver("192.0.2.1", "192.0.2.2"))
	config := &Config{EPDGAddr: "epdg.example:4500", EPDGCandidates: store, ProxyAddr: listener.Addr().String()}
	for _, want := range []string{"192.0.2.1", "192.0.2.2"} {
		assertCandidateSOCKSDestination(t, config, candidateSOCKSProbe{relay: relay, want: want})
		if err := <-served; err != nil {
			t.Fatal(err)
		}
	}
}

type candidateSOCKSProbe struct {
	relay *net.UDPConn
	want  string
}

func assertCandidateSOCKSDestination(t *testing.T, config *Config, probe candidateSOCKSProbe) {
	t.Helper()
	session := NewSession(config)
	if err := session.buildTransport(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer session.stopTransport()
	if err := session.socket.SendNATKeepalive(); err != nil {
		t.Fatal(err)
	}
	_ = probe.relay.SetReadDeadline(time.Now().Add(2 * time.Second))
	buffer := make([]byte, 64)
	length, _, err := probe.relay.ReadFromUDP(buffer)
	if err != nil || length != 11 || buffer[3] != 1 {
		t.Fatalf("SOCKS5 IPv4 datagram length=%d err=%v", length, err)
	}
	if got := net.IP(buffer[4:8]).String(); got != probe.want || binary.BigEndian.Uint16(buffer[8:10]) != 4500 {
		t.Fatalf("SOCKS5 actual destination=%s:%d, want %s:4500", got, binary.BigEndian.Uint16(buffer[8:10]), probe.want)
	}
	session.finishEPDGCandidate(addressRejection())
}

func serveCandidateSOCKSControl(conn net.Conn, relay *net.UDPAddr) error {
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, make([]byte, int(header[1]))); err != nil {
		return err
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return err
	}
	request := make([]byte, 10)
	if _, err := io.ReadFull(conn, request); err != nil {
		return err
	}
	if request[0] != 5 || request[1] != 3 || request[3] != 1 {
		return fmt.Errorf("unexpected SOCKS5 UDP associate header: %v", request[:4])
	}
	reply := []byte{5, 0, 0, 1, 127, 0, 0, 1}
	reply = binary.BigEndian.AppendUint16(reply, uint16(relay.Port))
	if _, err := conn.Write(reply); err != nil {
		return err
	}
	_, err := io.Copy(io.Discard, conn)
	return err
}
