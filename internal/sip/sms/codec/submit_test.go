package smscodec

import (
	"strings"
	"testing"

	"github.com/warthog618/sms"
	"github.com/warthog618/sms/encoding/tpdu"
)

func TestBuildSubmitTPDUsPreservesTextAndDestination(t *testing.T) {
	parts, err := BuildSubmitTPDUObjectsWithOptions("+447700900123", " hello ", SubmitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || parts[0].DA.Number() != "+447700900123" {
		t.Fatalf("parts = %+v", parts)
	}
	decoded, err := sms.Decode([]*tpdu.TPDU{&parts[0]})
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != " hello " {
		t.Fatalf("decoded text = %q", decoded)
	}
}

func TestBuildSubmitTPDUsUsesRealUCS2AndShortCodeTON(t *testing.T) {
	parts, err := BuildSubmitTPDUObjectsWithOptions("10086", "你好", SubmitOptions{Encoding: "ucs2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 {
		t.Fatalf("parts = %d", len(parts))
	}
	if parts[0].DCS != tpdu.DcsUCS2Data {
		t.Fatalf("DCS = 0x%02x", byte(parts[0].DCS))
	}
	if parts[0].DA.TypeOfNumber() != tpdu.TonUnknown || parts[0].DA.NumberingPlan() != tpdu.NpISDN {
		t.Fatalf("short-code address = %+v", parts[0].DA)
	}
	decoded, err := sms.Decode([]*tpdu.TPDU{&parts[0]})
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "你好" {
		t.Fatalf("decoded text = %q", decoded)
	}
}

func TestBuildSubmitTPDUsUsesProvidedConcatReference(t *testing.T) {
	parts, err := BuildSubmitTPDUObjectsWithOptions(
		"+447700900123", strings.Repeat("multipart ", 40),
		SubmitOptions{ConcatReference: 37},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) < 2 {
		t.Fatalf("parts = %d", len(parts))
	}
	for index := range parts {
		total, sequence, reference, ok := parts[index].ConcatInfo()
		if !ok || total != len(parts) || sequence != index+1 || reference != 37 {
			t.Fatalf("part %d concat=(%d,%d,%d,%v)", index+1, total, sequence, reference, ok)
		}
	}
}

func TestBuildSubmitTPDUsSetsStatusReportRequestBit(t *testing.T) {
	plain, _, err := BuildSubmitTPDUsWithOptions("85075", "INFO", SubmitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	withSRR, _, err := BuildSubmitTPDUsWithOptions("85075", "INFO", SubmitOptions{StatusReport: true})
	if err != nil {
		t.Fatal(err)
	}
	if plain[0][0]&0x20 != 0 {
		t.Fatalf("default first octet 0x%02x has TP-SRR set", plain[0][0])
	}
	if withSRR[0][0]&0x20 == 0 {
		t.Fatalf("status-report first octet 0x%02x missing TP-SRR", withSRR[0][0])
	}
	plain[0][0] |= 0x20
	if plain[0][0] != withSRR[0][0] {
		t.Fatalf("SRR path changed more than TP-SRR: default|0x20=0x%02x got=0x%02x", plain[0][0], withSRR[0][0])
	}
}

func TestSetSubmitMessageReference(t *testing.T) {
	encoded, _, err := BuildSubmitTPDUs("85075", "INFO")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := SetSubmitMessageReference(encoded[0], 0x5a)
	if err != nil {
		t.Fatal(err)
	}
	message := tpdu.TPDU{Direction: tpdu.MO}
	if err := message.UnmarshalBinary(updated); err != nil {
		t.Fatal(err)
	}
	if message.MR != 0x5a || message.SmsType() != tpdu.SmsSubmit {
		t.Fatalf("message = %+v", message)
	}
	if _, err := SetSubmitMessageReference([]byte{0xff}, 1); err == nil {
		t.Fatal("malformed TPDU did not fail")
	}
}
