package emergency

import (
	"testing"
)

func TestIsEmergencyDestination(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"112", true},
		{"911", true},
		{"999", true},
		{"110", true},
		{"tel:112", true},
		{"sip:911@example.com", true},
		{"urn:service:sos", true},
		{"urn:service:sos.fire", true},
		{"10086", false},
		{"", false},
		{"+8613800138000", false},
	}
	for _, tt := range tests {
		if got := IsEmergencyDestination(tt.in); got != tt.want {
			t.Errorf("IsEmergencyDestination(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestEmergencyEPDGAddr(t *testing.T) {
	addr := EmergencyEPDGAddr("310", "260")
	want := "sos.epdg.epc.mnc260.mcc310.pub.3gppnetwork.org"
	if addr != want {
		t.Errorf("EmergencyEPDGAddr = %q, want %q", addr, want)
	}
}

func TestPolicyDefaultDisabled(t *testing.T) {
	var p Policy
	if err := p.Check("112"); err != ErrOriginatingDisabled {
		t.Errorf("默认禁用时期望 ErrOriginatingDisabled，得到 %v", err)
	}
	p.Enabled = true
	if err := p.Check("112"); err != nil {
		t.Errorf("启用后不应报错: %v", err)
	}
	if err := p.Check("10086"); err != nil {
		t.Errorf("非紧急号码不应报错: %v", err)
	}
}
