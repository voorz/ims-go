package identity

import (
	"testing"

	"github.com/voorz/ims-go/internal/carrier"
)

func TestPrepareStartISIM(t *testing.T) {
	r := carrier.NewResolver(nil, nil)
	ident, cfg, err := PrepareStart(nil, PrepareInput{
		MCC:      "310",
		MNC:      "260",
		IMPI:     "user@mcc310.mnc260.3gppnetwork.org",
		IMPU:     "sip:user@example.com",
		Mode:     ModeISIM,
		Resolver: r,
	})
	if err != nil {
		t.Fatalf("PrepareStart: %v", err)
	}
	if ident.Mode != ModeISIM {
		t.Errorf("Mode = %s", ident.Mode)
	}
	if ident.IMPU != "sip:user@example.com" {
		t.Errorf("IMPU = %q", ident.IMPU)
	}
	if cfg == nil || cfg.MCC != "310" {
		t.Error("运营商配置错误")
	}
}

func TestPrepareStartDerived(t *testing.T) {
	r := carrier.NewResolver(nil, nil)
	ident, _, err := PrepareStart(nil, PrepareInput{
		MCC:      "460",
		MNC:      "00",
		Mode:     ModeDerived,
		Resolver: r,
	})
	if err != nil {
		t.Fatalf("PrepareStart: %v", err)
	}
	if ident.Mode != ModeDerived {
		t.Errorf("Mode = %s", ident.Mode)
	}
	if ident.IMPI == "" || ident.IMPU == "" {
		t.Error("推导身份为空")
	}
}

func TestPrepareStartAuto(t *testing.T) {
	r := carrier.NewResolver(nil, nil)
	// Auto 无身份 → 推导
	ident, _, err := PrepareStart(nil, PrepareInput{
		MCC: "310", MNC: "260", Mode: ModeAuto, Resolver: r,
	})
	if err != nil {
		t.Fatalf("PrepareStart: %v", err)
	}
	if ident.Mode != ModeDerived {
		t.Errorf("Auto 无身份时期望 Derived，得到 %s", ident.Mode)
	}
	// Auto 有身份 → ISIM
	ident2, _, err := PrepareStart(nil, PrepareInput{
		MCC: "310", MNC: "260", Mode: ModeAuto, Resolver: r,
		IMPI: "u@x", IMPU: "sip:u@x",
	})
	if err != nil {
		t.Fatalf("PrepareStart: %v", err)
	}
	if ident2.Mode != ModeISIM {
		t.Errorf("Auto 有身份时期望 ISIM，得到 %s", ident2.Mode)
	}
}

func TestPrepareStartValidation(t *testing.T) {
	r := carrier.NewResolver(nil, nil)
	_, _, err := PrepareStart(nil, PrepareInput{MCC: "", MNC: "260", Mode: ModeAuto, Resolver: r})
	if err == nil {
		t.Error("MCC 为空时期望错误")
	}
	_, _, err = PrepareStart(nil, PrepareInput{MCC: "310", MNC: "260", Mode: ModeAuto, Resolver: nil})
	if err == nil {
		t.Error("Resolver 为 nil 时期望错误")
	}
}
