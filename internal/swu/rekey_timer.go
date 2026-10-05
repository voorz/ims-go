package swu

import (
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/voorz/ims-go/internal/swu/ikev2"
)

const (
	// IR.51 4.4: 18-hour fallback when the ePDG omits AUTH_LIFETIME.
	defaultIKERekeyInterval   = 64800 * time.Second
	defaultChildRekeyInterval = 1800 * time.Second
	childRekeyStartOffset     = 30 * time.Second
	rekeyMaxFailures          = 2
	rekeyRetryInterval        = 60 * time.Second
)

type rekeyTimerSpec struct {
	name          string
	interval      time.Duration
	reset         <-chan struct{}
	target        **time.Timer
	action        func() error
	immediateFail func(error) bool
	retryInterval time.Duration
}

func (s *Session) rekeyIntervals() (time.Duration, time.Duration) {
	s.mu.RLock()
	lifetime := s.authLifetime
	s.mu.RUnlock()
	var ikeInterval, childInterval time.Duration
	if s.cfg != nil {
		ikeInterval = s.cfg.RekeyIKESeconds
		childInterval = s.cfg.RekeyChildSeconds
	}
	if ikeInterval <= 0 {
		ikeInterval = defaultIKERekeyInterval
		if lifetime > 0 {
			ikeInterval = time.Duration(lifetime) * time.Second * 4 / 5
		}
	}
	if childInterval > 0 {
		return ikeInterval, childInterval
	}
	childInterval = defaultChildRekeyInterval
	if lifetime > 0 {
		childInterval = time.Duration(lifetime) * time.Second * 7 / 8
	}
	return ikeInterval, childInterval + childRekeyStartOffset
}

// startIKESARekeyTimer restores the original interval-taking timer API.
func (s *Session) startIKESARekeyTimer(interval time.Duration) {
	reset := make(chan struct{}, 1)
	s.mu.Lock()
	s.rekeyResetCh = reset
	s.mu.Unlock()
	s.startRekeyTimer(rekeyTimerSpec{
		name: "IKE SA", interval: interval,
		reset: reset, target: &s.ikeRekeyTimer, action: s.RekeyIKESA,
	})
}

// startChildSARekeyTimer restores the original interval-taking timer API.
func (s *Session) startChildSARekeyTimer(interval time.Duration) {
	reset := make(chan struct{}, 1)
	s.mu.Lock()
	s.childRekeyResetCh = reset
	s.mu.Unlock()
	s.startRekeyTimer(rekeyTimerSpec{
		name: "CHILD_SA", interval: interval,
		reset: reset, target: &s.childRekeyTimer, action: s.RekeyChildSA,
		immediateFail: isChildSANotFoundError,
	})
}

func isChildSANotFoundError(err error) bool {
	var rejection *createChildSARejectError
	return errors.As(err, &rejection) && rejection.NotifyType == ikev2.CHILD_SA_NOT_FOUND
}

func (s *Session) startRekeyTimer(spec rekeyTimerSpec) {
	if spec.interval <= 0 || spec.action == nil || spec.target == nil || s.ctx.Err() != nil {
		return
	}
	timer := time.NewTimer(rekeyDelay(spec.interval))
	s.timersMu.Lock()
	if previous := *spec.target; previous != nil {
		previous.Stop()
	}
	*spec.target = timer
	s.timersMu.Unlock()
	s.rekeyTimerWG.Add(1)
	go func() {
		defer s.rekeyTimerWG.Done()
		s.runRekeyTimer(timer, spec)
	}()
}

func (s *Session) runRekeyTimer(timer *time.Timer, spec rekeyTimerSpec) {
	failures := 0
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-spec.reset:
			failures = 0
			if !s.resetRekeyTimer(timer, rekeyDelay(spec.interval)) {
				return
			}
		case <-timer.C:
			err := spec.action()
			if err == nil {
				failures = 0
				if !s.resetRekeyTimer(timer, rekeyDelay(spec.interval)) {
					return
				}
				continue
			}
			failures++
			if failures >= rekeyMaxFailures || spec.immediateFail != nil && spec.immediateFail(err) {
				s.failEstablishedControl(fmt.Errorf("swu: %s rekey failed: %w", spec.name, err))
				return
			}
			retryInterval := spec.retryInterval
			if retryInterval <= 0 {
				retryInterval = rekeyRetryInterval
			}
			if !s.resetRekeyTimer(timer, retryInterval) {
				return
			}
		}
	}
}

func (s *Session) resetRekeyTimer(timer *time.Timer, delay time.Duration) bool {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	if s.ctx.Err() != nil {
		return false
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
	return true
}

// rekeyDelay returns the next rekey wait in the IR.51 4.4 band of 75%–125%
// of the mean interval. Absolute one-sided jitter is not used.
func rekeyDelay(interval time.Duration) time.Duration {
	if interval <= 0 {
		return interval
	}
	minimum := interval * 3 / 4
	maximum := interval * 5 / 4
	span := maximum - minimum
	if span <= 0 {
		return interval
	}
	return minimum + time.Duration(rand.Int63n(int64(span)+1))
}
