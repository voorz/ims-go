package profile

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func parseYAML(data []byte, v interface{}) error {
	return yaml.Unmarshal(data, v)
}

// TestParseProfile 解析真实画像文件。
func TestParseProfile(t *testing.T) {
	data, err := os.ReadFile("/tmp/ios-carrier-profiles/profiles/2degrees_nz.yaml")
	if err != nil {
		t.Skipf("fixture 不可用: %v", err)
	}
	var p Profile
	if err := parseYAML(data, &p); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if p.ID != "apple_2degrees_nz" {
		t.Errorf("ID = %q", p.ID)
	}
	eff := p.Effective("53024")
	if eff.IMS.EPDG.Address != "epdg.ims.2degrees.net.nz" {
		t.Errorf("EPDG = %q", eff.IMS.EPDG.Address)
	}
	if !eff.IMS.Register.UseIPsec {
		t.Error("UseIPsec 应为 true")
	}
	if len(eff.IMS.IKE.Proposals) == 0 {
		t.Error("IKE proposals 不应为空")
	}
}

// TestSelectorMatch 测试 selector 匹配。
func TestSelectorMatch(t *testing.T) {
	m := &SelectorManifest{
		PLMN: "310280",
		Profiles: []SelectorProfile{
			{
				ID:   "apple_att_mvno_us",
				Path: "profiles/att_mvno_us.yaml",
				Selectors: []SelectorCondition{
					{Conditions: []Condition{
						{Field: "gid1", Match: "prefix", Value: "20FF"},
					}},
				},
			},
		},
	}
	sim := SIMIdentity{GID1: "20FF1234"}
	path := matchSelector(m, sim)
	if path != "profiles/att_mvno_us.yaml" {
		t.Errorf("path = %q", path)
	}
	// 不匹配
	sim2 := SIMIdentity{GID1: "9999"}
	if path := matchSelector(m, sim2); path != "" {
		t.Errorf("不应匹配，得到 %q", path)
	}
}

// TestNAIIdentifier 测试 NAI 模板展开（在 carrier 包）。
func TestNAIIdentifier(t *testing.T) {
	t.Skip("NAI 展开已移至 carrier 包内部")
}

// TestToCarrierConfig 测试映射（在 carrier 包）。
func TestToCarrierConfig(t *testing.T) {
	t.Skip("映射已移至 carrier 包内部")
}
