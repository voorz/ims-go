package swu

import (
	"log/slog"
	"time"

	"github.com/voorz/ims-go/internal/swu/ikev2"
)

const defaultReauthOverlapGrace = 10 * time.Second

func (s *Session) runtimeRedirectAddress(payloads []ikev2.Payload) string {
	for _, payload := range payloads {
		notify, ok := payload.(*ikev2.EncryptedPayloadNotify)
		if !ok || notify.NotifyType != ikev2.REDIRECT {
			continue
		}
		address, err := ParseRedirectData(notify.NotifyData)
		if err != nil {
			s.Logger.Warn("invalid runtime REDIRECT notification", "error", err)
			return ""
		}
		return address
	}
	return ""
}

func (s *Session) handleRuntimeRedirect(address string) {
	if address == "" {
		return
	}
	slog.Warn("SWu runtime REDIRECT received", "target", address)
	if s.OnRedirect != nil {
		go s.OnRedirect(address)
	}
	if s.OnSessionDown != nil {
		s.notifySessionDown()
		return
	}
	s.cancel()
}

func (s *Session) triggerReauthentication() {
	// RFC 7296 2.8.3: reauthentication starts a new IKE_SA_INIT/IKE_AUTH.
	// This session never injects EAP onto the existing IKE SA. When a host
	// callback is present it keeps forwarding until that host deletes us
	// after the successor IKE and Child SA exist.
	if s.OnReauthNeeded != nil {
		go s.OnReauthNeeded()
		retry := s.reauthOverlapGrace
		if retry <= 0 {
			retry = defaultReauthOverlapGrace
		}
		s.armTimer(&s.ikeReauthTimer, retry, func() {
			if s.ctx.Err() == nil && s.State() == stateEstablished {
				s.triggerReauthentication()
			}
		})
		return
	}
	grace := s.reauthOverlapGrace
	if grace <= 0 {
		grace = defaultReauthOverlapGrace
	}
	s.armTimer(&s.ikeReauthTimer, grace, func() {
		if s.ctx.Err() == nil {
			s.finishReauthentication()
		}
	})
}

func (s *Session) finishReauthentication() {
	s.sendEstablishedDeletes()
	s.failEstablishedControl(ErrFreshRuntimeRequired)
}
