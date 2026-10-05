package ipsec3gpp

import (
	"encoding/binary"
	"sync/atomic"
	"time"
)

// These are packet metadata only. Never retain plaintext, keys or SIP bodies.
type FlowPacketDiagnostics struct {
	Packets            uint64    `json:"packets"`
	UDP                uint64    `json:"udp"`
	SYN                uint64    `json:"syn"`
	SYNACK             uint64    `json:"syn_ack"`
	ACK                uint64    `json:"ack"`
	RST                uint64    `json:"rst"`
	TransformErrors    uint64    `json:"transform_errors"`
	ReplayRejected     uint64    `json:"replay_rejected"`
	SelectorMismatches uint64    `json:"selector_mismatches"`
	LastPacketAt       time.Time `json:"last_packet_at"`
}

type FlowDiagnostics struct {
	LocalPort   int                   `json:"local_port"`
	RemotePort  int                   `json:"remote_port"`
	InboundSPI  uint32                `json:"inbound_spi"`
	OutboundSPI uint32                `json:"outbound_spi"`
	Inbound     FlowPacketDiagnostics `json:"inbound_decoded"`
	Outbound    FlowPacketDiagnostics `json:"outbound_protected"`
}

type TransportDiagnostics struct {
	LocalIP            string                `json:"local_ip"`
	RemoteIP           string                `json:"remote_ip"`
	Stats              TransportStats        `json:"stats"`
	UnknownInboundSPI  uint64                `json:"unknown_inbound_spi"`
	LastUnknownInbound *UnknownESPDiagnostic `json:"last_unknown_inbound,omitempty"`
	FlowC              FlowDiagnostics       `json:"flow_c"`
	FlowS              FlowDiagnostics       `json:"flow_s"`
}

// Header observations are unverified, not evidence of successful decryption.
// Keep only the latest observation, never packet contents or security keys.
type UnknownESPDiagnostic struct {
	Source      string    `json:"source"`
	Destination string    `json:"destination"`
	SPI         uint32    `json:"spi"`
	Sequence    uint32    `json:"sequence"`
	ObservedAt  time.Time `json:"observed_at"`
}

func (transport *Transport) recordUnknownESP(packet ipPacket) {
	transport.lastUnknownInbound.Store(&UnknownESPDiagnostic{
		Source: packet.source.String(), Destination: packet.destination.String(),
		SPI:      binary.BigEndian.Uint32(packet.payload[:4]),
		Sequence: binary.BigEndian.Uint32(packet.payload[4:8]), ObservedAt: time.Now(),
	})
}

type flowPacketCounters struct {
	packets, syn, synACK, ack, rst atomic.Uint64
	udp                            atomic.Uint64
	transformErrors, replay        atomic.Uint64
	selectorMismatches             atomic.Uint64
	lastPacketAt                   atomic.Int64
}

const (
	tcpFlagSYN       = 0x02
	tcpFlagRST       = 0x04
	tcpFlagACK       = 0x10
	tcpHeaderMinimum = 20
)

func (flow *transportFlow) observePacket(protocol byte, payload []byte, inbound bool) {
	d := flow.diagnostics
	d.packets.Add(1)
	d.lastPacketAt.Store(time.Now().UnixNano())
	if protocol != protocolTCP && protocol != protocolUDP {
		return
	}
	flow.observePorts(payload, inbound)
	if protocol == protocolTCP {
		d.observeTCPFlags(payload)
	} else {
		d.udp.Add(1)
	}
}

func (flow *transportFlow) observePorts(payload []byte, inbound bool) {
	d := flow.diagnostics
	if len(payload) < 4 {
		d.selectorMismatches.Add(1)
		return
	}
	local, remote := binary.BigEndian.Uint16(payload[:2]), binary.BigEndian.Uint16(payload[2:4])
	if inbound {
		local, remote = remote, local
	}
	if int(local) != flow.flow.LocalPort || int(remote) != flow.flow.RemotePort {
		d.selectorMismatches.Add(1)
	}
}

func (d *flowPacketCounters) observeTCPFlags(payload []byte) {
	if len(payload) < tcpHeaderMinimum {
		return
	}
	if headerLength := int(payload[12]>>4) * 4; headerLength < tcpHeaderMinimum || headerLength > len(payload) {
		return
	}
	flags := payload[13]
	switch flags & (tcpFlagSYN | tcpFlagACK) {
	case tcpFlagSYN:
		d.syn.Add(1)
	case tcpFlagSYN | tcpFlagACK:
		d.synACK.Add(1)
	case tcpFlagACK:
		d.ack.Add(1)
	}
	if flags&tcpFlagRST != 0 {
		d.rst.Add(1)
	}
}

func (d *flowPacketCounters) snapshot() FlowPacketDiagnostics {
	result := FlowPacketDiagnostics{
		Packets: d.packets.Load(), SYN: d.syn.Load(), SYNACK: d.synACK.Load(),
		UDP: d.udp.Load(),
		ACK: d.ack.Load(), RST: d.rst.Load(), TransformErrors: d.transformErrors.Load(),
		ReplayRejected: d.replay.Load(), SelectorMismatches: d.selectorMismatches.Load(),
	}
	if at := d.lastPacketAt.Load(); at != 0 {
		result.LastPacketAt = time.Unix(0, at)
	}
	return result
}

func (transport *Transport) Diagnostics() TransportDiagnostics {
	return TransportDiagnostics{
		LocalIP: transport.policy.LocalIP.String(), RemoteIP: transport.policy.RemoteIP.String(),
		Stats: transport.Stats(), UnknownInboundSPI: transport.unknownInboundSPI.Load(),
		LastUnknownInbound: transport.lastUnknownInbound.Load(),
		FlowC:              transport.flowDiagnostics(transport.policy.FlowC),
		FlowS:              transport.flowDiagnostics(transport.policy.FlowS),
	}
}

func (transport *Transport) flowDiagnostics(flow Flow) FlowDiagnostics {
	result := FlowDiagnostics{
		LocalPort: flow.LocalPort, RemotePort: flow.RemotePort,
		InboundSPI: flow.InboundSPI, OutboundSPI: flow.OutboundSPI,
	}
	if inbound := transport.inbound[flow.InboundSPI]; inbound != nil {
		result.Inbound = inbound.diagnostics.snapshot()
	}
	for i := range transport.outbound {
		outbound := &transport.outbound[i]
		if outbound.sa.SPI == flow.OutboundSPI {
			result.Outbound = outbound.diagnostics.snapshot()
		}
	}
	return result
}
