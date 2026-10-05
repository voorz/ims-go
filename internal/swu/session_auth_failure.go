package swu

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/voorz/ims-go/internal/swu/ikev2"
)

// Local cleanup budget, not an IKE protocol timer. Context cancellation
// interrupts the wait; unsuccessful Delete exchanges remain visible in the error.
const rejectedIKEDeleteTimeout = 5 * time.Second

func (s *Session) markRejectedIKEForDelete(err error, payloads []ikev2.Payload) {
	var rejection *IKEAuthError
	if !errors.As(err, &rejection) || rejection.NotifyType != ikev2.INTERNAL_ADDRESS_FAILURE {
		return
	}
	if !s.eapSuccessReceived || len(s.eapKeys.MSK) == 0 || (s.cfg != nil && s.cfg.DisableEAPMACValidation) {
		return
	}
	if s.cfg != nil && s.cfg.VerifyFinalResponderAUTH && s.verifyEAPResponderAuth(payloads) != nil {
		return
	}
	// Only the decrypted final response after mutual EAP reaches this point.
	// Do not publish established/readiness or delete an offered, uncreated child.
	rejection.deleteCandidateIKE = true
}

func (s *Session) cleanupConnectAttempt(ctx context.Context, cause error) error {
	deleteErr := s.deleteRejectedCandidateIKE(ctx, cause)
	err := errors.Join(cause, deleteErr, s.stopDataPlane(), s.stopIKEControl())
	s.stopTransport()
	return err
}

func (s *Session) deleteRejectedCandidateIKE(ctx context.Context, cause error) error {
	var rejection *IKEAuthError
	if !errors.As(cause, &rejection) || !rejection.deleteCandidateIKE || ctx.Err() != nil {
		return nil
	}
	var deleteErr error
	s.deleteOnce.Do(func() {
		cleanupCtx, cancel := context.WithTimeout(ctx, rejectedIKEDeleteTimeout)
		defer cancel()
		if err := s.exchangeRejectedIKEDelete(cleanupCtx); err != nil {
			deleteErr = fmt.Errorf("swu: delete candidate IKE SA after address rejection: %w", err)
			s.Logger.Warn("IKE address rejection cleanup failed", "notify_type", rejection.NotifyType, "error", deleteErr)
			return
		}
		s.Logger.Info("IKE address rejection cleanup acknowledged", "notify_type", rejection.NotifyType)
	})
	return deleteErr
}

func (s *Session) exchangeRejectedIKEDelete(ctx context.Context) error {
	// Use the handshake exchange path so the initial socket write failure is
	// returned directly, before waiting/retransmitting the same Delete request.
	if err := s.sendDeleteIKE(); err != nil {
		return err
	}
	response, err := s.receiveIKE(ctx)
	if err != nil {
		return err
	}
	payloads, err := s.decryptAndParse(response)
	if err != nil {
		return err
	}
	if len(payloads) != 0 {
		return fmt.Errorf("swu: IKE SA delete response contains payloads %s", ikePayloadTypes(payloads))
	}
	return nil
}
