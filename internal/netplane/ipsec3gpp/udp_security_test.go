package ipsec3gpp

import (
	"encoding/binary"
	"net"
	"testing"
)

func TestProtectedUDPRejectsPlaintextAndFirstFragments(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		local, remote := net.IPv4(10, 0, 0, 2), net.IPv4(10, 0, 0, 1)
		if ipv6 {
			local, remote = net.ParseIP("2001:db8::2"), net.ParseIP("2001:db8::1")
		}
		ue, _ := newTransportPair(t, EncryptionAES, local, remote)
		packet := udpPacket(t, remote, 51000, local, 41001, []byte("MESSAGE payload"))
		if ipv6 {
			packet = ipv6ExtensionUDPPacket(t, remote, 51000, local, 41001, []byte("MESSAGE payload"), false)
		}
		for _, fragmented := range []bool{false, true} {
			wire := append([]byte(nil), packet...)
			if fragmented && !ipv6 {
				binary.BigEndian.PutUint16(wire[6:8], 0x2000)
			}
			if fragmented && ipv6 {
				wire = ipv6ExtensionUDPPacket(t, remote, 51000, local, 41001, []byte("MESSAGE payload"), true)
				// First fragment with M=1 instead of the fixture's nonzero offset.
				binary.BigEndian.PutUint16(wire[50:52], 1)
			}
			if out, _, err := ue.TransformInbound(wire); err == nil || out != nil {
				t.Fatalf("plaintext UDP accepted: ipv6=%t fragmented=%t", ipv6, fragmented)
			}
		}
	}
}

func TestProtectedUDPAuthenticatesBeforeDeliveryAndCountsDatagrams(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		local, remote := net.IPv4(10, 0, 0, 2), net.IPv4(10, 0, 0, 1)
		if ipv6 {
			local, remote = net.ParseIP("2001:db8::2"), net.ParseIP("2001:db8::1")
		}
		ue, server := newTransportPair(t, EncryptionAES, local, remote)
		packet := udpPacket(t, remote, 51000, local, 41001, []byte("MESSAGE payload"))
		if ipv6 {
			packet = ipv6ExtensionUDPPacket(t, remote, 51000, local, 41001, []byte("MESSAGE payload"), false)
		}
		wire, protected, err := server.TransformOutbound(packet)
		if err != nil || !protected {
			t.Fatalf("protect UDP: %v", err)
		}
		tampered := append([]byte(nil), wire...)
		tampered[len(tampered)-1] ^= 0xff
		if out, _, err := ue.TransformInbound(tampered); err == nil || out != nil {
			t.Fatal("tampered UDP accepted")
		}
		if _, _, err := ue.TransformInbound(wire); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ue.TransformInbound(wire); err == nil {
			t.Fatal("replayed UDP accepted")
		}
		d := ue.Diagnostics()
		if d.FlowS.Inbound.UDP != 1 || d.FlowS.Inbound.Packets != 1 {
			t.Fatalf("UDP evidence = %+v", d.FlowS)
		}
	}
}

func TestUnknownESPRecordsOnlyUnverifiedHeaderMetadata(t *testing.T) {
	ue, server := newTransportPair(t, EncryptionAES, nil, nil)
	wire, _, err := server.TransformOutbound(udpPacket(t, server.policy.LocalIP, 51000, ue.policy.LocalIP, 41001, []byte("secret text")))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseIPPacket(wire)
	if err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint32(wire[parsed.headerLength:], 123456789)
	if _, _, err := ue.TransformInbound(wire); err == nil {
		t.Fatal("unknown SPI accepted")
	}
	d := ue.Diagnostics()
	if d.LastUnknownInbound == nil || d.LastUnknownInbound.SPI != 123456789 ||
		d.LastUnknownInbound.Source != server.policy.LocalIP.String() || d.LastUnknownInbound.ObservedAt.IsZero() || d.FlowS.Inbound.Packets != 0 {
		t.Fatalf("unknown SPI diagnostic = %+v", d)
	}
}
