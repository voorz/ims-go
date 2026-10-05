package swu

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/voorz/ims-go/internal/swu/crypto"
	"github.com/voorz/ims-go/internal/swu/ikev2"
)

func TestConnectRecordsProtectedIKEAuthAddressRejection(t *testing.T) {
	store := NewEPDGCandidateStore(candidateResolver("192.0.2.1", "192.0.2.2"))
	for _, want := range []string{"192.0.2.1", "192.0.2.2"} {
		transport := newTestIKETransport()
		session := NewSession(&Config{
			EPDGAddr: "epdg.example", EPDGCandidates: store, IMSI: "001010123456789",
			TransportFactory: func(_, remote string) (Transport, error) {
				host, _, _ := net.SplitHostPort(remote)
				transport.remoteIP = net.ParseIP(host)
				return transport, nil
			},
		})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		result := make(chan error, 1)
		go func() { result <- session.Connect(ctx) }()
		rejectTestIKEAuth(t, session, transport)
		err := <-result
		cancel()
		var rejection *IKEAuthError
		if !errors.As(err, &rejection) || rejection.NotifyType != ikev2.INTERNAL_ADDRESS_FAILURE {
			t.Fatalf("Connect did not retain protected Notify 36: %v", err)
		}
		if transport.remoteIP.String() != want || !transport.stopped.Load() || session.Snapshot().Established {
			t.Fatalf("target=%s want=%s stopped=%t", transport.remoteIP, want, transport.stopped.Load())
		}
		session.Shutdown()
	}
}

func rejectTestIKEAuth(t *testing.T, session *Session, transport *testIKETransport) {
	t.Helper()
	_ = receiveFragmentPacket(t, transport.sentIKE)
	responderDH, err := crypto.NewDiffieHellman(14)
	if err != nil {
		t.Fatal(err)
	}
	transport.ike <- encodeInitPacket(t, buildInitResp(t, session, responderDH))
	request, err := ikev2.DecodePacket(receiveFragmentPacket(t, transport.sentIKE))
	if err != nil || request.Header.ExchangeType != ikev2.IKE_AUTH {
		t.Fatalf("expected IKE_AUTH: %v %v", request, err)
	}
	response := &ikev2.IKEPacket{
		Header:   newIKEHeader(session.spiI, session.spiR, ikev2.IKE_AUTH, ikeResponseFlag, request.Header.MessageID),
		Payloads: []ikev2.Payload{&ikev2.EncryptedPayloadNotify{NotifyType: ikev2.INTERNAL_ADDRESS_FAILURE}},
	}
	raw, err := session.encryptAndWrap(response)
	if err != nil {
		t.Fatal(err)
	}
	transport.ike <- raw
}

func TestEPDGCandidatesLeaveDirectTransportResolutionUnchanged(t *testing.T) {
	store := NewEPDGCandidateStore(func(context.Context, string, string) (*net.UDPAddr, []net.IP, error) {
		t.Error("SOCKS5 candidate selection replaced the direct socket's own DNS policy")
		return nil, nil, errors.New("unexpected resolver call")
	})
	config := &Config{EPDGAddr: "127.0.0.1", EpDGPort: 4500, LocalAddr: "127.0.0.1", EPDGCandidates: store}
	session := NewSession(config)
	if err := session.buildTransport(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer session.stopTransport()
	if session.epdgCandidate != nil || session.socket.RemoteIP().String() != "127.0.0.1" || session.socket.RemotePort() != 4500 {
		t.Fatalf("direct transport endpoint=%s:%d", session.socket.RemoteIP(), session.socket.RemotePort())
	}
	if config.EPDGAddr != "127.0.0.1" || config.EpDGPort != 4500 {
		t.Fatal("candidate selection changed the caller's endpoint")
	}
}
