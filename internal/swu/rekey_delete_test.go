package swu

import (
	"encoding/binary"
	"testing"

	"github.com/voorz/ims-go/internal/swu/ikev2"
)

func TestPeerDeleteOfActiveChildSAIsAcknowledgedAndSurfaced(t *testing.T) {
	session, transport := newEstablishedControlSession(t)
	defer stopControlTestSession(session)
	session.controlMu.Lock()
	session.controlRunning = false
	session.controlMu.Unlock()
	request := encryptedPeerDeleteRequest(t, session, ikev2.ProtoESP, spiBytes(session.espLocalSPI))
	err := session.handlePeerInformational(request)
	if err == nil || err.Error() != "swu: peer deleted the active CHILD_SA" {
		t.Fatalf("handlePeerInformational error = %v", err)
	}
	assertChildSADeleteResponse(t, session, transport, session.espRemoteSPI)
}

func TestPeerDeleteOfActiveIKESAIsAcknowledgedAndSurfaced(t *testing.T) {
	session, transport := newEstablishedControlSession(t)
	defer stopControlTestSession(session)
	session.controlMu.Lock()
	session.controlRunning = false
	session.controlMu.Unlock()
	request := encryptedPeerDeleteRequest(t, session, ikev2.ProtoIKE, nil)
	err := session.handlePeerInformational(request)
	if err == nil || err.Error() != "swu: peer deleted the active IKE_SA" {
		t.Fatalf("handlePeerInformational error = %v", err)
	}
	raw := receiveFragmentPacket(t, transport.sentIKE)
	packet, decodeErr := ikev2.DecodePacket(raw)
	if decodeErr != nil {
		t.Fatalf("decode IKE Delete response: %v", decodeErr)
	}
	payloads, decryptErr := session.decryptAndParse(packet)
	if decryptErr != nil || len(payloads) != 0 {
		t.Fatalf("IKE Delete response payloads=%d err=%v", len(payloads), decryptErr)
	}
}

func TestShutdownSendsDeleteChildThenIKEWhileKeysLive(t *testing.T) {
	session, transport := newEstablishedControlSession(t)
	session.sendEstablishedDeletes()
	childRaw := receiveFragmentPacket(t, transport.sentIKE)
	childPacket, err := ikev2.DecodePacket(childRaw)
	if err != nil {
		t.Fatalf("decode CHILD Delete: %v", err)
	}
	childPayloads, err := session.decryptAndParse(childPacket)
	if err != nil || len(childPayloads) != 1 {
		t.Fatalf("CHILD Delete payloads=%d err=%v", len(childPayloads), err)
	}
	childDelete := childPayloads[0].(*ikev2.EncryptedPayloadDelete)
	if childDelete.ProtocolID != ikev2.ProtoESP {
		t.Fatalf("first Delete protocol = %d, want ESP", childDelete.ProtocolID)
	}
	ikeRaw := receiveFragmentPacket(t, transport.sentIKE)
	ikePacket, err := ikev2.DecodePacket(ikeRaw)
	if err != nil {
		t.Fatalf("decode IKE Delete: %v", err)
	}
	ikePayloads, err := session.decryptAndParse(ikePacket)
	if err != nil || len(ikePayloads) != 1 {
		t.Fatalf("IKE Delete payloads=%d err=%v", len(ikePayloads), err)
	}
	ikeDelete := ikePayloads[0].(*ikev2.EncryptedPayloadDelete)
	if ikeDelete.ProtocolID != ikev2.ProtoIKE {
		t.Fatalf("second Delete protocol = %d, want IKE", ikeDelete.ProtocolID)
	}
	session.Shutdown()
}

func TestShutdownInvokesEstablishedDeletes(t *testing.T) {
	session, transport := newEstablishedControlSession(t)
	session.Shutdown()
	for i := 0; i < 2; i++ {
		raw := receiveFragmentPacket(t, transport.sentIKE)
		packet, err := ikev2.DecodePacket(raw)
		if err != nil || packet.Header.ExchangeType != ikev2.INFORMATIONAL {
			t.Fatalf("shutdown packet %d type=%v err=%v", i, packet, err)
		}
	}
}

func TestShutdownSkipsDeleteWhenSessionNeverEstablished(t *testing.T) {
	session := NewSession(&Config{})
	session.Shutdown()
}

func TestSendDeleteChildSAEncodesEverySPI(t *testing.T) {
	session, transport := newEstablishedControlSession(t)
	defer stopControlTestSession(session)
	want := []uint32{0x01020304, 0x11121314, 0xa1a2a3a4}
	if err := session.sendDeleteChildSA(want); err != nil {
		t.Fatalf("sendDeleteChildSA: %v", err)
	}
	raw := receiveFragmentPacket(t, transport.sentIKE)
	packet, err := ikev2.DecodePacket(raw)
	if err != nil {
		t.Fatalf("decode Delete request: %v", err)
	}
	payloads, err := session.decryptAndParse(packet)
	if err != nil || len(payloads) != 1 {
		t.Fatalf("Delete payloads=%d err=%v", len(payloads), err)
	}
	deletion := payloads[0].(*ikev2.EncryptedPayloadDelete)
	if deletion.NumSPIs != uint16(len(want)) || len(deletion.SPIs) != 4*len(want) {
		t.Fatalf("Delete SPI metadata = %d/%d", deletion.NumSPIs, len(deletion.SPIs))
	}
	for index, wantSPI := range want {
		if got := binary.BigEndian.Uint32(deletion.SPIs[index*4:]); got != wantSPI {
			t.Fatalf("Delete SPI %d = %08x, want %08x", index, got, wantSPI)
		}
	}
}

func TestValidateChildSADeleteResponseRequiresExpectedSPI(t *testing.T) {
	matching := childSADeleteResponse([]uint32{0x01020304})
	if err := validateChildSADeleteResponse(matching, 0x01020304); err != nil {
		t.Fatalf("matching Delete response: %v", err)
	}
	if err := validateChildSADeleteResponse(nil, 0x01020304); err != nil {
		t.Fatalf("empty Delete response: %v", err)
	}
	if err := validateChildSADeleteResponse(matching, 0x11121314); err == nil {
		t.Fatal("mismatched Delete response should fail")
	}
}

func encryptedPeerDeleteRequest(
	t *testing.T,
	session *Session,
	protocol ikev2.ProtocolID,
	spis []byte,
) *ikev2.IKEPacket {
	t.Helper()
	spiSize := uint8(0)
	if len(spis) > 0 {
		spiSize = 4
	}
	request := &ikev2.IKEPacket{
		InitiatorSPI: session.spiI, ResponderSPI: session.spiR,
		Version: 0x20, ExchangeType: ikev2.ExchangeInformational, MessageID: 91,
		Payloads: []ikev2.Payload{&ikev2.EncryptedPayloadDelete{
			ProtocolID: protocol, SPISize: spiSize, NumSPIs: uint16(len(spis) / 4), SPIs: spis,
		}},
	}
	raw, err := session.encryptAndWrap(request)
	if err != nil {
		t.Fatalf("encrypt peer Delete: %v", err)
	}
	packet, err := ikev2.DecodePacket(raw)
	if err != nil {
		t.Fatalf("decode peer Delete: %v", err)
	}
	return packet
}
