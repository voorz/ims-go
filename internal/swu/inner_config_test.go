package swu

import (
	"net"
	"strings"
	"testing"

	"github.com/voorz/ims-go/internal/swu/ikev2"
)

func TestParseAssignedInnerConfigRejectsMissingReply(t *testing.T) {
	_, err := parseAssignedInnerConfig([]ikev2.Payload{
		&ikev2.EncryptedPayloadAuth{AuthMethod: ikev2.AuthMethodDigitalSignature},
	})
	if err == nil || !strings.Contains(err.Error(), "omitted CFG_REPLY (payloads=39)") {
		t.Fatalf("parseAssignedInnerConfig error = %v", err)
	}
}

func TestParseAssignedInnerConfigReportsReplyAttributesWithoutAddress(t *testing.T) {
	cp := &ikev2.EncryptedPayloadCP{
		CFGType: ikev2.CFG_REPLY,
		Attributes: []*ikev2.CPAttribute{
			{Type: ikev2.CPAttrIP4DNS, Value: net.IPv4(1, 1, 1, 1).To4()},
		},
	}
	_, err := parseAssignedInnerConfig([]ikev2.Payload{cp})
	if err == nil || !strings.Contains(err.Error(), "attributes=3:4") {
		t.Fatalf("parseAssignedInnerConfig error = %v", err)
	}
}

func TestParseAssignedInnerConfigAcceptsIPv4Reply(t *testing.T) {
	cp := &ikev2.EncryptedPayloadCP{
		CFGType: ikev2.CFG_REPLY,
		Attributes: []*ikev2.CPAttribute{
			{Type: ikev2.CPAttrIP4Address, Value: net.IPv4(10, 0, 0, 8).To4()},
		},
	}
	config, err := parseAssignedInnerConfig([]ikev2.Payload{cp})
	if err != nil {
		t.Fatalf("parseAssignedInnerConfig: %v", err)
	}
	if got := config.ipv4.String(); got != "10.0.0.8" {
		t.Fatalf("assigned IPv4 = %q", got)
	}
}

func TestParseAssignedInnerConfigAcceptsIPv6OnlyReply(t *testing.T) {
	ipv6 := append(net.ParseIP("2001:db8::8").To16(), byte(64))
	dns := net.ParseIP("2001:db8::53").To16()
	pcscf := net.ParseIP("2001:db8::5060").To16()
	cp := &ikev2.EncryptedPayloadCP{
		CFGType: ikev2.CFG_REPLY,
		Attributes: []*ikev2.CPAttribute{
			{Type: ikev2.CPAttrIP6Address, Value: ipv6},
			{Type: ikev2.CPAttrIP6DNS, Value: dns},
			{Type: ikev2.CPAttrPCSCFIP6, Value: pcscf},
		},
	}
	config, err := parseAssignedInnerConfig([]ikev2.Payload{cp})
	if err != nil {
		t.Fatalf("parseAssignedInnerConfig: %v", err)
	}
	if !config.ipv6.Equal(net.ParseIP("2001:db8::8")) || config.ipv6Prefix != 64 {
		t.Fatalf("assigned IPv6 = %s/%d", config.ipv6, config.ipv6Prefix)
	}
	if len(config.dns) != 1 || !config.dns[0].Equal(dns) || len(config.pcscf) != 1 || !config.pcscf[0].Equal(pcscf) {
		t.Fatalf("assigned DNS/P-CSCF = %v/%v", config.dns, config.pcscf)
	}
}

func TestParseAssignedInnerConfigAcceptsConcatenatedPCSCFIPv6(t *testing.T) {
	first := net.ParseIP("2001:db8::10").To16()
	second := net.ParseIP("2001:db8::11").To16()
	cp := &ikev2.EncryptedPayloadCP{
		CFGType: ikev2.CFG_REPLY,
		Attributes: []*ikev2.CPAttribute{
			{Type: ikev2.CPAttrIP4Address, Value: net.IPv4(10, 0, 0, 8).To4()},
			{Type: ikev2.ASSIGNED_PCSCF_IP6_ADDRESS, Value: append(append([]byte(nil), first...), second...)},
		},
	}
	config, err := parseAssignedInnerConfig([]ikev2.Payload{cp})
	if err != nil {
		t.Fatalf("parseAssignedInnerConfig: %v", err)
	}
	if len(config.pcscf) != 2 || !config.pcscf[0].Equal(first) || !config.pcscf[1].Equal(second) {
		t.Fatalf("P-CSCF = %v", config.pcscf)
	}
}

func TestParseAssignedInnerConfigAcceptsMultiplePCSCFIPv6Attributes(t *testing.T) {
	first := net.ParseIP("2001:db8::10").To16()
	second := net.ParseIP("2001:db8::11").To16()
	cp := &ikev2.EncryptedPayloadCP{
		CFGType: ikev2.CFG_REPLY,
		Attributes: []*ikev2.CPAttribute{
			{Type: ikev2.CPAttrIP6Address, Value: append(net.ParseIP("2001:db8::8").To16(), 64)},
			{Type: ikev2.ASSIGNED_PCSCF_IP6_ADDRESS, Value: first},
			{Type: ikev2.ASSIGNED_PCSCF_IP6_ADDRESS, Value: second},
		},
	}
	config, err := parseAssignedInnerConfig([]ikev2.Payload{cp})
	if err != nil {
		t.Fatalf("parseAssignedInnerConfig: %v", err)
	}
	if len(config.pcscf) != 2 || !config.pcscf[0].Equal(first) || !config.pcscf[1].Equal(second) {
		t.Fatalf("P-CSCF = %v", config.pcscf)
	}
}

func TestParseAssignedInnerConfigIgnoresEmptyAndTrailingPCSCFIPv6(t *testing.T) {
	first := net.ParseIP("2001:db8::10").To16()
	cp := &ikev2.EncryptedPayloadCP{
		CFGType: ikev2.CFG_REPLY,
		Attributes: []*ikev2.CPAttribute{
			{Type: ikev2.CPAttrIP4Address, Value: net.IPv4(10, 0, 0, 8).To4()},
			{Type: ikev2.ASSIGNED_PCSCF_IP6_ADDRESS},
			{Type: ikev2.ASSIGNED_PCSCF_IP6_ADDRESS, Value: append(append([]byte(nil), first...), 1, 2, 3, 4)},
		},
	}
	config, err := parseAssignedInnerConfig([]ikev2.Payload{cp})
	if err != nil {
		t.Fatalf("parseAssignedInnerConfig: %v", err)
	}
	if len(config.pcscf) != 1 || !config.pcscf[0].Equal(first) {
		t.Fatalf("P-CSCF = %v", config.pcscf)
	}
}

func TestIKEAuthenticationErrorReportsAddressFailure(t *testing.T) {
	wire := ikev2.EncodePayloadChain([]ikev2.Payload{
		&ikev2.EncryptedPayloadNotify{NotifyType: 36},
	})
	payloads, err := ikev2.DecodePayloadChainWithFirst(ikev2.PayloadNotify, wire)
	if err != nil {
		t.Fatalf("decode Notify: %v", err)
	}
	err = ikeAuthenticationError(payloads)
	if err == nil || !strings.Contains(err.Error(), "INTERNAL_ADDRESS_FAILURE (36)") {
		t.Fatalf("ikeAuthenticationError = %v", err)
	}
	if rejection, ok := err.(*IKEAuthError); !ok || rejection.NotifyType != ikev2.INTERNAL_ADDRESS_FAILURE {
		t.Fatalf("IKE_AUTH Notify lost its type: %T %v", err, err)
	}
}
