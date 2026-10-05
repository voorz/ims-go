package swu

import (
	"time"
)

const sessionStatsInterval = 60 * time.Second

func (s *Session) startSessionStats() {
	s.sessionStatsOnce.Do(func() {
		s.sessionStatsWG.Add(1)
		go func() {
			defer s.sessionStatsWG.Done()
			s.logSessionStats(sessionStatsInterval)
		}()
	})
}

// logSessionStats restores the original interval-taking lifecycle symbol. The
// original logger calls were commented out; this implementation exposes the
// same live state without packet or key material.
func (s *Session) logSessionStats(interval time.Duration) {
	if s == nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.logSessionStatsSnapshot()
		}
	}
}

func (s *Session) logSessionStatsSnapshot() {
	s.mu.RLock()
	state, startedAt := s.state, s.startedAt
	dataPlaneStarted := s.dataPlaneStarted
	s.mu.RUnlock()
	s.Logger.Debug("SWu session stats",
		"state", state,
		"messageID", s.SequenceNumber.Load(),
		"dataPlaneStarted", dataPlaneStarted, "uptime", time.Since(startedAt),
	)
}
