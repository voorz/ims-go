package ipsec3gpp

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

func TestTransportDiagnosticsDistinguishesPortSHandshake(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		local, remote := net.IPv4(10, 0, 0, 2), net.IPv4(10, 0, 0, 1)
		if ipv6 {
			local, remote = net.ParseIP("2001:db8::2"), net.ParseIP("2001:db8::1")
		}
		ue, server := newTransportPair(t, EncryptionAES, local, remote)
		deliverDiagnosticPacket(t, [2]*Transport{server, ue}, diagnosticTCPPacket(t, [2]net.IP{remote, local}, tcpFlagSYN))
		synACK := diagnosticTCPPacket(t, [2]net.IP{local, remote}, tcpFlagSYN|tcpFlagACK)
		deliverDiagnosticPacket(t, [2]*Transport{ue, server}, synACK)
		deliverDiagnosticPacket(t, [2]*Transport{server, ue}, diagnosticTCPPacket(t, [2]net.IP{remote, local}, tcpFlagACK))
		d := ue.Diagnostics()
		if d.FlowS.Inbound.SYN != 1 || d.FlowS.Inbound.ACK != 1 || d.FlowS.Outbound.SYNACK != 1 {
			t.Fatalf("IPv6=%v handshake diagnostics: %+v", ipv6, d)
		}
		if d.FlowS.LocalPort != 41001 || d.FlowS.RemotePort != 51000 ||
			d.FlowC.Inbound.Packets != 0 || d.FlowC.Outbound.Packets != 0 {
			t.Fatalf("flow attribution lost: %+v", d)
		}
		encoded, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"private SMS body", `"CK"`, `"IK"`, `"ck"`, `"ik"`} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("diagnostics retained payload or keys: %s", secret)
			}
		}
	}
}

func TestTransportDiagnosticsSeparatesRejectedInboundPackets(t *testing.T) {
	ue, server := newTransportPair(t, EncryptionAES, nil, nil)
	packet := diagnosticTCPPacket(t, [2]net.IP{server.policy.LocalIP, ue.policy.LocalIP}, tcpFlagSYN)
	wire, _, err := server.TransformOutbound(packet)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ue.TransformInbound(wire); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ue.TransformInbound(wire); err == nil {
		t.Fatal("replay accepted")
	}
	tampered := append([]byte(nil), wire...)
	tampered[len(tampered)-1] ^= 0xff
	if _, _, err := ue.TransformInbound(tampered); err == nil {
		t.Fatal("tampered packet accepted")
	}
	unknown := append([]byte(nil), wire...)
	parsed, err := parseIPPacket(unknown)
	if err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint32(unknown[parsed.headerLength:], 0xdeadbeef)
	if _, _, err := ue.TransformInbound(unknown); err == nil {
		t.Fatal("unknown SPI accepted")
	}
	d := ue.Diagnostics()
	if d.UnknownInboundSPI != 1 || d.FlowS.Inbound.TransformErrors != 1 ||
		d.FlowS.Inbound.ReplayRejected != 1 || d.FlowS.Inbound.SYN != 1 {
		t.Fatalf("rejected traffic counted as successful handshake: %+v", d)
	}
}

func diagnosticTCPPacket(t *testing.T, addresses [2]net.IP, flags byte) []byte {
	t.Helper()
	source, destination := addresses[0], addresses[1]
	localPort, remotePort := uint16(41001), uint16(51000)
	if source.Equal(net.IPv4(10, 0, 0, 1)) || source.Equal(net.ParseIP("2001:db8::1")) {
		localPort, remotePort = remotePort, localPort
	}
	tcp := make([]byte, tcpHeaderMinimum)
	binary.BigEndian.PutUint16(tcp[:2], localPort)
	binary.BigEndian.PutUint16(tcp[2:4], remotePort)
	tcp[12], tcp[13] = 5<<4, flags
	tcp = append(tcp, []byte("private SMS body")...)
	packet := udpPacket(t, source, localPort, destination, remotePort, nil)
	if source.To4() == nil {
		packet = ipv6ExtensionUDPPacket(t, source, localPort, destination, remotePort, nil, false)
	}
	parsed, err := parseIPPacket(packet)
	if err != nil {
		t.Fatal(err)
	}
	result, err := replaceIPPayload(payloadReplacement{packet: packet, parsed: parsed, protocol: protocolTCP, payload: tcp})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func deliverDiagnosticPacket(t *testing.T, peers [2]*Transport, packet []byte) {
	t.Helper()
	wire, transformed, err := peers[0].TransformOutbound(packet)
	if err != nil || !transformed {
		t.Fatalf("protect diagnostic packet: %v, %v", transformed, err)
	}
	if _, transformed, err := peers[1].TransformInbound(wire); err != nil || !transformed {
		t.Fatalf("decode diagnostic packet: %v, %v", transformed, err)
	}
}
