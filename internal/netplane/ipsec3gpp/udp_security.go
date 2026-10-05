package ipsec3gpp

import "encoding/binary"

// UnprotectedUDPDestination identifies the first plaintext fragment as well as
// complete UDP datagrams. It is used before installing an SA to guard reserved
// receive ports; ESP packets are deliberately not classified as plaintext UDP.
func UnprotectedUDPDestination(packet []byte) (string, int) {
	parsed, err := parseIPPacket(packet)
	if err != nil || parsed.protocol != protocolUDP || len(parsed.payload) < 4 ||
		(parsed.fragmented && !initialIPFragment(parsed, packet)) {
		return "", 0
	}
	return parsed.destination.String(), int(binary.BigEndian.Uint16(parsed.payload[2:4]))
}

// Reject the first plaintext fragment too; otherwise IP reassembly would bypass
// protection at the negotiated UDP server port. Unrelated traffic is unchanged.
func (transport *Transport) isUnprotectedServerUDP(parsed ipPacket, packet []byte) bool {
	if parsed.protocol != protocolUDP || !ipEqual(parsed.destination, transport.policy.LocalIP) || len(parsed.payload) < 4 {
		return false
	}
	if parsed.fragmented && !initialIPFragment(parsed, packet) {
		return false
	}
	return int(binary.BigEndian.Uint16(parsed.payload[2:4])) == transport.policy.LocalPortS
}

func initialIPFragment(parsed ipPacket, packet []byte) bool {
	if parsed.version == 4 {
		return binary.BigEndian.Uint16(packet[6:8])&0x1fff == 0
	}
	// parseIPPacket has already validated all extension header lengths.
	protocol, offset := packet[6], 40
	for isIPv6ExtensionHeader(protocol) {
		if protocol == 44 {
			return binary.BigEndian.Uint16(packet[offset+2:offset+4])&0xfff8 == 0
		}
		protocol, offset = packet[offset], offset+(int(packet[offset+1])+1)*8
	}
	return true
}

func (flow *transportFlow) matchesInboundUDP(payload []byte) bool {
	return len(payload) >= 8 &&
		int(binary.BigEndian.Uint16(payload[:2])) == flow.flow.RemotePort &&
		int(binary.BigEndian.Uint16(payload[2:4])) == flow.flow.LocalPort
}
