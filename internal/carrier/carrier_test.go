package carrier

import (
	"testing"
)

func TestResolvePreset(t *testing.T) {
	r := NewResolver(nil, nil)
	cfg, err := r.ResolveEffectiveCarrierConfig("310", "260")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.Name != "T-Mobile US" {
		t.Errorf("Name = %q", cfg.Name)
	}
	if cfg.EPDG == "" {
		t.Error("EPDG 为空")
	}
}

func TestResolveDerived(t *testing.T) {
	r := NewResolver(nil, nil)
	cfg, err := r.ResolveEffectiveCarrierConfig("999", "99")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.Name != "Derived" {
		t.Errorf("Name = %q，期望 Derived", cfg.Name)
	}
	if cfg.EPDG == "" {
		t.Error("推导 EPDG 为空")
	}
}

func TestOverrideJSON(t *testing.T) {
	r := NewResolver(nil, nil)
	data := []byte(`{"mcc":"310","mnc":"260","name":"Custom","epdg":"custom.example.com"}`)
	if err := r.LoadOverrideJSON(data); err != nil {
		t.Fatalf("LoadOverrideJSON: %v", err)
	}
	cfg, err := r.ResolveEffectiveCarrierConfig("310", "260")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.Name != "Custom" || cfg.EPDG != "custom.example.com" {
		t.Errorf("覆盖未生效: %+v", cfg)
	}
}

// memStore 是内存 LearnedProfileStore（测试用）。
type memStore struct {
	m map[string]*LearnedProfile
}

func (s *memStore) Load(key string) (*LearnedProfile, error) {
	return s.m[key], nil
}

func (s *memStore) Save(p *LearnedProfile) error {
	s.m[p.Key] = p
	return nil
}

func TestLearnedPriority(t *testing.T) {
	store := &memStore{m: make(map[string]*LearnedProfile)}
	r := NewResolver(nil, store)
	// 保存已学习的档案（优先级高于 preset）
	_ = store.Save(&LearnedProfile{
		Key:     "310260",
		Version: 2,
		Config:  CarrierConfig{MCC: "310", MNC: "260", Name: "Learned", EPDG: "learned.example.com"},
	})
	cfg, err := r.ResolveEffectiveCarrierConfig("310", "260")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.Name != "Learned" {
		t.Errorf("已学习优先级未生效: %q", cfg.Name)
	}
}

func TestIsVoWiFiBlockedMCC(t *testing.T) {
	r := NewResolver(nil, nil)
	if r.IsVoWiFiBlockedMCC("310") {
		t.Error("310 不应被阻止")
	}
}
