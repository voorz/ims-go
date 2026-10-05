package swu

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

const dataPlaneStatsInterval = 30 * time.Second

// InnerPacketSnapshot is the original public userspace data-plane snapshot.
type InnerPacketSnapshot struct {
	OutboundPackets     uint64
	InboundPackets      uint64
	SAMisses            uint64
	EncapsulationErrors uint64
	DecapsulationErrors uint64
	SendErrors          uint64
	LastOutboundLen     uint64
	LastInboundLen      uint64
}

type dataPlaneRuntimeStats struct {
	tunRead        atomic.Uint64
	espSend        atomic.Uint64
	espSendError   atomic.Uint64
	espOutSAMiss   atomic.Uint64
	espEncapError  atomic.Uint64
	espIn          atomic.Uint64
	espInSAMiss    atomic.Uint64
	espDecapError  atomic.Uint64
	tunWrite       atomic.Uint64
	tunWriteError  atomic.Uint64
	lastInSPI      atomic.Uint32
	lastPlainLen   atomic.Uint64
	lastTunReadLen atomic.Uint64
}

type dataPlaneStatsSnapshot struct {
	tunRead, espSend, espSendError       uint64
	espOutSAMiss, espEncapError, espIn   uint64
	espInSAMiss, espDecapError, tunWrite uint64
	tunWriteError                        uint64
	lastInSPI                            uint32
	lastPlainLen, lastTunReadLen         uint64
	innerPacket                          InnerPacketSnapshot
}

func (s *dataPlaneRuntimeStats) snapshot() dataPlaneStatsSnapshot {
	snapshot := dataPlaneStatsSnapshot{
		tunRead: s.tunRead.Load(), espSend: s.espSend.Load(),
		espSendError: s.espSendError.Load(), espOutSAMiss: s.espOutSAMiss.Load(),
		espEncapError: s.espEncapError.Load(), espIn: s.espIn.Load(),
		espInSAMiss: s.espInSAMiss.Load(), espDecapError: s.espDecapError.Load(),
		tunWrite: s.tunWrite.Load(), tunWriteError: s.tunWriteError.Load(),
		lastInSPI: s.lastInSPI.Load(), lastPlainLen: s.lastPlainLen.Load(),
		lastTunReadLen: s.lastTunReadLen.Load(),
	}
	snapshot.innerPacket = projectInnerPacketSnapshot(snapshot)
	return snapshot
}

func (s *dataPlaneRuntimeStats) innerPacketSnapshot() InnerPacketSnapshot {
	return s.snapshot().innerPacket
}

func projectInnerPacketSnapshot(s dataPlaneStatsSnapshot) InnerPacketSnapshot {
	return InnerPacketSnapshot{
		OutboundPackets:     s.espSend,
		InboundPackets:      s.tunWrite,
		SAMisses:            s.espOutSAMiss + s.espInSAMiss,
		EncapsulationErrors: s.espEncapError,
		DecapsulationErrors: s.espDecapError,
		SendErrors:          s.espSendError,
		LastOutboundLen:     s.lastTunReadLen,
		LastInboundLen:      s.lastPlainLen,
	}
}

func (s *dataPlaneRuntimeStats) recordEncapsulationError(err error) {
	if errors.Is(err, errInnerPacketSAMissing) {
		s.espOutSAMiss.Add(1)
		return
	}
	s.espEncapError.Add(1)
}

func (s *dataPlaneRuntimeStats) recordDecapsulationError(err error) {
	if errors.Is(err, errInnerPacketSAMissing) {
		s.espInSAMiss.Add(1)
		return
	}
	s.espDecapError.Add(1)
}

func (s *Session) logDataPlaneStats(
	ctx context.Context,
	mode string,
	stats *dataPlaneRuntimeStats,
	interval time.Duration,
) {
	if s == nil || ctx == nil || stats == nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.logDataPlaneStatsSnapshot(mode, stats.snapshot())
		}
	}
}

func (s *Session) logDataPlaneStatsSnapshot(mode string, snapshot dataPlaneStatsSnapshot) {
	s.Logger.Debug("SWu dataplane stats",
		"mode", mode, "tunRead", snapshot.tunRead,
		"espSend", snapshot.espSend, "espSendError", snapshot.espSendError,
		"espOutSAMiss", snapshot.espOutSAMiss, "espEncapError", snapshot.espEncapError,
		"espIn", snapshot.espIn, "espInSAMiss", snapshot.espInSAMiss,
		"espDecapError", snapshot.espDecapError, "tunWrite", snapshot.tunWrite,
		"tunWriteError", snapshot.tunWriteError, "lastInSPI", snapshot.lastInSPI,
		"lastPlainLen", snapshot.lastPlainLen, "lastTunReadLen", snapshot.lastTunReadLen,
	)
}
