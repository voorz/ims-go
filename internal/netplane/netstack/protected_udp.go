package netstack

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"

	"github.com/voorz/ims-go/internal/netplane/ipsec3gpp"
)

type protectedPacketConn struct {
	net.PacketConn
	release func()
	once    sync.Once
}

func (conn *protectedPacketConn) Close() error {
	err := conn.PacketConn.Close()
	conn.once.Do(conn.release)
	return err
}

// Install the plaintext filter before binding, including while no SA exists.
// This prevents pre-authentication datagrams from being queued for later use.
func (a *IMSNetworkAdapter) ListenProtectedUDP(addr *net.UDPAddr) (net.PacketConn, error) {
	if addr == nil || addr.IP == nil || addr.Port <= 0 || addr.Port > 65535 || a.network.bridge == nil {
		return nil, errors.New("netstack: protected UDP requires a bound address and packet bridge")
	}
	bridge, key := a.network.bridge, addr.String()
	bridge.mu.Lock()
	if bridge.protectedUDPPorts == nil {
		bridge.protectedUDPPorts = make(map[string]int)
	}
	bridge.protectedUDPPorts[key]++
	bridge.mu.Unlock()
	release := func() {
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		bridge.protectedUDPPorts[key]--
		if bridge.protectedUDPPorts[key] == 0 {
			delete(bridge.protectedUDPPorts, key)
		}
	}
	conn, err := a.network.ListenPacket(context.Background(), "udp", addr)
	if err != nil {
		release()
		return nil, err
	}
	return &protectedPacketConn{PacketConn: conn, release: release}, nil
}

func (bridge *PacketBridge) rejectsPlaintextUDP(packet []byte) bool {
	bridge.mu.RLock()
	defer bridge.mu.RUnlock()
	if len(bridge.protectedUDPPorts) == 0 {
		return false
	}
	ip, port := ipsec3gpp.UnprotectedUDPDestination(packet)
	return bridge.protectedUDPPorts[net.JoinHostPort(ip, strconv.Itoa(port))] > 0
}
