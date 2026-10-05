package swu

import (
	"context"
	"fmt"
	"log/slog"
)

type xfrmExpireEvent struct {
	spi  uint32
	hard bool
}

type kernelXFRMExpireMonitor interface {
	StartExpireMonitor(context.Context, func(xfrmExpireEvent)) error
}

// startXFRMExpireMonitor restores the original post-establishment monitor.
func (s *Session) startXFRMExpireMonitor() {
	monitor, ok := s.currentKernelDataPlane().(kernelXFRMExpireMonitor)
	if !ok {
		return
	}
	if err := monitor.StartExpireMonitor(s.ctx, s.handleXFRMExpire); err != nil {
		slog.Warn("start XFRM expire monitor failed", "error", err)
		return
	}
	slog.Info("XFRM SA expire monitor started")
}

func (s *Session) handleXFRMExpire(event xfrmExpireEvent) {
	state, ok := s.currentKernelDataPlane().(kernelSPIState)
	if !ok {
		return
	}
	outbound, inbound := state.CurrentSPIs()
	if event.spi != outbound && event.spi != inbound {
		return
	}
	if event.hard {
		s.handleXFRMHardExpire(event.spi)
		return
	}
	slog.Info("XFRM SA soft expired", "spi", event.spi)
	s.launchXFRMSoftExpireRekey(event.spi)
}

func (s *Session) handleXFRMHardExpire(spi uint32) {
	slog.Warn("XFRM SA hard expired", "spi", spi)
	if s.OnSessionDown != nil {
		s.notifySessionDown()
		return
	}
	s.failEstablishedControl(fmt.Errorf("swu: XFRM SA hard expired: spi=%08x", spi))
}

func (s *Session) launchXFRMSoftExpireRekey(spi uint32) {
	s.xfrmActionMu.Lock()
	if s.xfrmActionClosing || s.ctx.Err() != nil {
		s.xfrmActionMu.Unlock()
		return
	}
	s.xfrmActionWG.Add(1)
	s.xfrmActionMu.Unlock()
	go func() {
		defer s.xfrmActionWG.Done()
		rekey := s.RekeyChildSA
		if s.xfrmRekey != nil {
			rekey = s.xfrmRekey
		}
		if err := rekey(); err != nil && s.ctx.Err() == nil {
			slog.Warn("XFRM soft-expire CHILD_SA rekey failed",
				"spi", spi, "error", err)
		}
	}()
}

func (s *Session) stopXFRMActions() {
	s.xfrmActionMu.Lock()
	s.xfrmActionClosing = true
	s.xfrmActionMu.Unlock()
	s.xfrmActionWG.Wait()
}
