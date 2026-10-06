package carrier

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/voorz/ims-go/internal/carrier/profile"
)

func TestNAIIdentifier(t *testing.T) {
	tmpl := "0$imsi@nai.epc.mnc$mnc.mcc$mcc.3gppnetwork.org"
	got := naiIdentifier(tmpl, "234150123456789", "234", "15")
	want := "0234150123456789@nai.epc.mnc15.mcc234.3gppnetwork.org"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestProfileToConfig(t *testing.T) {
	data, err := os.ReadFile("/tmp/ios-carrier-profiles/profiles/2degrees_nz.yaml")
	if err != nil {
		t.Skipf("fixture 不可用: %v", err)
	}
	var p profile.Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		t.Fatalf("解析: %v", err)
	}
	cfg := profileToConfig(&p, "53024", "530", "24")
	if cfg.EPDG != "epdg.ims.2degrees.net.nz" {
		t.Errorf("EPDG = %q", cfg.EPDG)
	}
	if cfg.MCC != "530" || cfg.MNC != "24" {
		t.Errorf("MCC/MNC = %q/%q", cfg.MCC, cfg.MNC)
	}
}
