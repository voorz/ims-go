package swu

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/voorz/ims-go/internal/swu/ikev2"
)

func TestAddressRejectionDeletesOnlyCandidateIKEBeforeTransportClose(t *testing.T) {
	session, transport := newAddressRejectionSession(t)
	old, oldTransport := newEstablishedControlSession(t)
	t.Cleanup(func() { stopControlTestSession(old) })
	cause := finalAddressRejection(t, session, nil)
	if err := session.startIKEControl(); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- session.cleanupConnectAttempt(context.Background(), cause) }()
	request := readCandidateDelete(t, session, transport)
	if transport.stopped.Load() {
		t.Fatal("transport closed before candidate IKE Delete was acknowledged")
	}
	replyToCandidateDelete(t, session, request)
	err := <-result
	if !errors.Is(err, cause) || err.Error() != cause.Error() || !transport.stopped.Load() {
		t.Fatalf("cleanup err=%v stopped=%t", err, transport.stopped.Load())
	}
	if oldTransport.stopped.Load() || oldTransport.sendCount.Load() != 0 || !old.Snapshot().Established {
		t.Fatal("candidate cleanup affected the old established session")
	}
	session.Shutdown()
	if transport.sendCount.Load() != 1 || session.Snapshot().Established {
		t.Fatalf("Delete repeated or rejected candidate became ready: sends=%d", transport.sendCount.Load())
	}
}

func TestAddressRejectionCleanupPreservesSendFailure(t *testing.T) {
	session, transport := newAddressRejectionSession(t)
	sendErr := errors.New("candidate socket write failed")
	transport.sendIKEErr = sendErr
	cause := finalAddressRejection(t, session, nil)
	err := session.cleanupConnectAttempt(context.Background(), cause)
	if !errors.Is(err, cause) || !errors.Is(err, sendErr) || !transport.stopped.Load() {
		t.Fatalf("cleanup lost cause or failed to close transport: %v", err)
	}
}

func TestAddressRejectionCleanupPreservesTimeout(t *testing.T) {
	session, transport := newAddressRejectionSession(t)
	cause := finalAddressRejection(t, session, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := session.cleanupConnectAttempt(ctx, cause)
	if !errors.Is(err, cause) || !errors.Is(err, context.DeadlineExceeded) || !transport.stopped.Load() {
		t.Fatalf("cleanup timeout hidden: %v", err)
	}
}

func TestAddressRejectionDeleteRetransmitsSamePacket(t *testing.T) {
	session, transport := newAddressRejectionSession(t)
	session.cfg.Retransmit = &RetransmitConfig{MaxRetries: 1, InitialDelay: 100 * time.Millisecond, Backoff: 1}
	cause := finalAddressRejection(t, session, nil)
	result := make(chan error, 1)
	go func() { result <- session.cleanupConnectAttempt(context.Background(), cause) }()
	first := receiveFragmentPacket(t, transport.sentIKE)
	second := receiveFragmentPacket(t, transport.sentIKE)
	if !bytes.Equal(first, second) {
		t.Fatal("Delete retransmission changed message ID or protected payload")
	}
	request, err := ikev2.DecodePacket(second)
	if err != nil {
		t.Fatal(err)
	}
	request.Payloads = nil
	replyToCandidateDelete(t, session, request)
	if err := <-result; err.Error() != cause.Error() {
		t.Fatalf("Delete retransmission failed: %v", err)
	}
}

func TestAddressRejectionParentCancellationDoesNotWaitForDelete(t *testing.T) {
	session, transport := newAddressRejectionSession(t)
	cause := finalAddressRejection(t, session, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := session.cleanupConnectAttempt(ctx, cause)
	if !errors.Is(err, cause) || transport.sendCount.Load() != 0 || !transport.stopped.Load() {
		t.Fatalf("parent cancellation changed: err=%v sends=%d", err, transport.sendCount.Load())
	}
}

func TestAddressRejectionCleanupTimeoutDoesNotPreserveResumeMaterial(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		session := NewSession(&Config{ResumeTicket: []byte("ticket"), ResumeOldSKd: []byte("old-SKd")})
		var cause error = context.DeadlineExceeded
		if rejected {
			cause = errors.Join(&IKEAuthError{NotifyType: ikev2.INTERNAL_ADDRESS_FAILURE}, cause)
		}
		session.finishConnectFailure(cause)
		_, _, complete := session.sessionResumptionCredentials()
		if complete == rejected || !errors.Is(session.TerminalError(), cause) {
			t.Fatalf("rejected=%t resume material preserved=%t err=%v", rejected, complete, session.TerminalError())
		}
		session.Shutdown()
	}
}

func TestAddressRejectionCleanupRejectsNonemptyDeleteResponse(t *testing.T) {
	session, transport := newAddressRejectionSession(t)
	cause := finalAddressRejection(t, session, nil)
	result := make(chan error, 1)
	go func() { result <- session.cleanupConnectAttempt(context.Background(), cause) }()
	request := readCandidateDelete(t, session, transport)
	request.Payloads = []ikev2.Payload{&ikev2.EncryptedPayloadNotify{NotifyType: ikev2.MOBIKE_SUPPORTED}}
	replyToCandidateDelete(t, session, request)
	err := <-result
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "delete response contains payloads") {
		t.Fatalf("invalid Delete response accepted: %v", err)
	}
}

func TestAddressRejectionDoesNotDeleteBeforeMutualEAP(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Session)
	}{
		{"missing-success", func(s *Session) { s.eapSuccessReceived = false }},
		{"missing-MSK", func(s *Session) { s.eapKeys.MSK = nil }},
		{"unverified-MAC", func(s *Session) { s.cfg.DisableEAPMACValidation = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, transport := newAddressRejectionSession(t)
			test.change(session)
			cause := finalAddressRejection(t, session, nil)
			err := session.cleanupConnectAttempt(context.Background(), cause)
			if !errors.Is(err, cause) || transport.sendCount.Load() != 0 {
				t.Fatalf("premature Delete: err=%v sends=%d", err, transport.sendCount.Load())
			}
		})
	}
}

func TestAddressRejectionDoesNotDeleteOnStrictProofFailure(t *testing.T) {
	session, transport := newAddressRejectionSession(t)
	session.cfg.VerifyFinalResponderAUTH = true
	cause := finalAddressRejection(t, session, []ikev2.Payload{
		&ikev2.EncryptedPayloadAuth{AuthMethod: ikev2.AuthMethodSharedKey, AuthData: []byte("bad proof")},
	})
	err := session.cleanupConnectAttempt(context.Background(), cause)
	if !errors.Is(err, cause) || transport.sendCount.Load() != 0 {
		t.Fatalf("invalid strict AUTH caused Delete: %v", err)
	}
}

func TestOtherConnectFailuresDoNotSendCandidateDelete(t *testing.T) {
	for _, cause := range []error{
		&IKEAuthError{NotifyType: ikev2.INTERNAL_ADDRESS_FAILURE}, // Initial, pre-EAP response.
		&IKEAuthError{NotifyType: ikev2.AUTHENTICATION_FAILED},
		context.DeadlineExceeded, errors.New("transport unavailable"),
	} {
		session, transport := newAddressRejectionSession(t)
		err := session.cleanupConnectAttempt(context.Background(), cause)
		if !errors.Is(err, cause) || transport.sendCount.Load() != 0 || !transport.stopped.Load() {
			t.Fatalf("unrelated failure changed: err=%v sends=%d", err, transport.sendCount.Load())
		}
	}
}

func newAddressRejectionSession(t *testing.T) (*Session, *testIKETransport) {
	t.Helper()
	session, _ := finalIKEAuthResponseWithInvalidProof(t, false)
	transport := newTestIKETransport()
	session.socket = transport
	session.espLocalSPI = 0x10203040 // Offered, not established: must not send CHILD_SA Delete.
	session.nextOutboundID = 4
	copy(session.spiI[:], []byte("init-spi"))
	copy(session.spiR[:], []byte("resp-spi"))
	t.Cleanup(func() { session.cleanupResources(false) })
	return session, transport
}

func finalAddressRejection(t *testing.T, session *Session, extra []ikev2.Payload) error {
	t.Helper()
	payloads := append([]ikev2.Payload{&ikev2.EncryptedPayloadNotify{NotifyType: ikev2.INTERNAL_ADDRESS_FAILURE}}, extra...)
	response := &ikev2.IKEPacket{
		Header: newIKEHeader(session.spiI, session.spiR, ikev2.IKE_AUTH, ikeResponseFlag, 3), Payloads: payloads,
	}
	raw, err := session.encryptAndWrap(response)
	if err != nil {
		t.Fatal(err)
	}
	err = session.handleIKEAuthFinalResp(raw)
	var rejection *IKEAuthError
	if !errors.As(err, &rejection) || rejection.NotifyType != ikev2.INTERNAL_ADDRESS_FAILURE {
		t.Fatalf("final IKE_AUTH rejection=%v", err)
	}
	return err
}

func readCandidateDelete(t *testing.T, session *Session, transport *testIKETransport) *ikev2.IKEPacket {
	t.Helper()
	packet, err := ikev2.DecodePacket(receiveFragmentPacket(t, transport.sentIKE))
	if err != nil {
		t.Fatal(err)
	}
	if packet.Header.ExchangeType != ikev2.INFORMATIONAL || packet.Header.MessageID != 4 {
		t.Fatalf("Delete header=%+v", packet.Header)
	}
	payloads, err := session.decryptAndParse(packet)
	if err != nil || len(payloads) != 1 {
		t.Fatalf("Delete payloads=%v err=%v", payloads, err)
	}
	deletion, ok := payloads[0].(*ikev2.EncryptedPayloadDelete)
	if !ok || deletion.ProtocolID != ikev2.ProtoIKE || deletion.NumSPIs != 0 || deletion.SPISize != 0 {
		t.Fatalf("not an IKE-only Delete: %#v", payloads[0])
	}
	packet.Payloads = nil
	return packet
}

func replyToCandidateDelete(t *testing.T, session *Session, request *ikev2.IKEPacket) {
	t.Helper()
	response := &ikev2.IKEPacket{
		Header:   newIKEHeader(session.spiI, session.spiR, ikev2.INFORMATIONAL, ikeResponseFlag, request.Header.MessageID),
		Payloads: request.Payloads,
	}
	raw, err := session.encryptAndWrap(response)
	if err != nil {
		t.Fatal(err)
	}
	session.transport().(*testIKETransport).ike <- raw
}
