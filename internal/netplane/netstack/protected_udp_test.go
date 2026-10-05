package netstack

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/voorz/ims-go/internal/netplane/ipsec3gpp"
)

func newProtectedUDPNetwork(t *testing.T, ip net.IP) (*IMSNetworkAdapter, *testInnerEndpoint) {
	t.Helper()
	endpoint := newTestInnerEndpoint()
	var ip4, ip6 net.IP
	if ip.To4() != nil {
		ip4 = ip
	} else {
		ip6 = ip
	}
	network, err := NewNetwork(context.Background(), ip4, ip6, 64, defaultMTU, endpoint, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = network.Close() })
	return AdaptIMSNetwork(network), endpoint
}

func TestProtectedUDPBridgeRejectsQueuedPlaintextAndDeliversAuthenticatedPackets(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		t.Run(map[bool]string{false: "IPv4", true: "IPv6"}[ipv6], func(t *testing.T) {
			policy := testIPSecPolicy()
			if ipv6 {
				policy.LocalIP, policy.RemoteIP = net.ParseIP("2001:db8::2"), net.ParseIP("2001:db8::1")
			}
			ue, _ := newProtectedUDPNetwork(t, policy.LocalIP)
			server, endpoint := newProtectedUDPNetwork(t, policy.RemoteIP)
			destination := &net.UDPAddr{IP: policy.LocalIP, Port: int(policy.LocalServerPort)}
			receiver, err := ue.ListenProtectedUDP(destination)
			if err != nil {
				t.Fatal(err)
			}
			defer receiver.Close()
			sender, err := server.ListenPacket("udp", &net.UDPAddr{IP: policy.RemoteIP, Port: int(policy.RemoteClientPort)})
			if err != nil {
				t.Fatal(err)
			}
			defer sender.Close()
			plain := writeProtectedUDPTestPacket(t, sender, destination, endpoint)
			if err := ue.network.bridge.injectInboundPacket(plain); err == nil {
				t.Fatal("pre-authentication packet entered reserved socket")
			}
			if err := ue.InstallIPSec3GPP(policy); err != nil {
				t.Fatal(err)
			}
			if err := server.InstallIPSec3GPP(reverseProtectedTestPolicy(policy)); err != nil {
				t.Fatal(err)
			}
			wire := writeProtectedUDPTestPacket(t, sender, destination, endpoint)
			if err := ue.network.bridge.injectInboundPacket(wire); err != nil {
				t.Fatal(err)
			}
			if err := receiver.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			buffer := make([]byte, 100)
			n, remote, err := receiver.ReadFrom(buffer)
			if err != nil || string(buffer[:n]) != "protected test" || remote.String() != sender.LocalAddr().String() {
				t.Fatalf("protected datagram delivery: n=%d remote=%v err=%v", n, remote, err)
			}
			d := ue.network.IMSNetworkDiagnostics()["ipsec"].(ipsec3gpp.TransportDiagnostics)
			if d.FlowS.Inbound.UDP != 1 {
				t.Fatal("datagram bypassed the negotiated SA")
			}
			if err := ue.network.bridge.injectInboundPacket(wire); err == nil {
				t.Fatal("replayed packet accepted")
			}
		})
	}
}

func reverseProtectedTestPolicy(policy ipsec3gpp.Policy) ipsec3gpp.Policy {
	policy.LocalIP, policy.RemoteIP = policy.RemoteIP, policy.LocalIP
	policy.LocalClientPort, policy.RemoteClientPort = policy.RemoteClientPort, policy.LocalClientPort
	policy.LocalServerPort, policy.RemoteServerPort = policy.RemoteServerPort, policy.LocalServerPort
	policy.LocalClientSPI, policy.RemoteClientSPI = policy.RemoteClientSPI, policy.LocalClientSPI
	policy.LocalServerSPI, policy.RemoteServerSPI = policy.RemoteServerSPI, policy.LocalServerSPI
	return policy
}

func writeProtectedUDPTestPacket(t *testing.T, sender net.PacketConn, destination *net.UDPAddr, endpoint *testInnerEndpoint) []byte {
	t.Helper()
	if _, err := sender.WriteTo([]byte("protected test"), destination); err != nil {
		t.Fatal(err)
	}
	select {
	case wire := <-endpoint.writes:
		return wire
	case <-time.After(time.Second):
		t.Fatal("UDP packet did not reach the tunnel")
	}
	return nil
}

func TestProtectedUDPReservationFailureAndCloseKeepFilterOwnership(t *testing.T) {
	network, _ := newProtectedUDPNetwork(t, net.IPv4(10, 0, 0, 2))
	address := &net.UDPAddr{IP: network.LocalIP(), Port: 41001}
	conn, err := network.ListenProtectedUDP(address)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := network.ListenProtectedUDP(address); err == nil {
		_ = duplicate.Close()
		t.Fatal("duplicate protected UDP reservation accepted")
	}
	if network.network.bridge.protectedUDPPorts[address.String()] != 1 {
		t.Fatal("failed reservation removed live filter")
	}
	_ = conn.Close()
	_ = conn.Close()
	if len(network.network.bridge.protectedUDPPorts) != 0 {
		t.Fatal("closed reservation retained its filter")
	}
}
