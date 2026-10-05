package carrier

import (
	"testing"
	"time"
)

func TestDeriveConverge(t *testing.T) {
	store := &memStore{m: make(map[string]*LearnedProfile)}
	r := NewResolver(nil, store)
	e := NewDeriveEngine(nil, r)

	var decisions []string
	e.OnDecision = func(d, detail string) {
		decisions = append(decisions, d)
	}
	// 第二个变体成功
	calls := 0
	e.OnProbe = func(v ParamVariant) ProbeResult {
		calls++
		if v.Name == "no-ipsec" {
			return ProbeResult{Variant: v, Success: true, Detail: "REGISTER 200", At: time.Now()}
		}
		return ProbeResult{Variant: v, Success: false, Detail: "REGISTER 403", At: time.Now()}
	}

	cfg, err := e.Derive("310", "260")
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if calls != 2 {
		t.Errorf("试探次数 = %d，期望 2", calls)
	}
	if cfg == nil {
		t.Error("cfg 为 nil")
	}
	// 验证已持久化
	profile, _ := store.Load("310260")
	if profile == nil {
		t.Error("已学习档案未持久化")
	}
	// 验证决策记录
	found := false
	for _, d := range decisions {
		if d == "derive-converged" {
			found = true
		}
	}
	if !found {
		t.Error("缺少 derive-converged 决策记录")
	}
}

func TestDeriveGiveUp(t *testing.T) {
	r := NewResolver(nil, nil)
	e := NewDeriveEngine(nil, r)
	e.OnProbe = func(v ParamVariant) ProbeResult {
		return ProbeResult{Variant: v, Success: false, Detail: "fail"}
	}
	_, err := e.Derive("999", "99")
	if err == nil {
		t.Error("全部失败时期望错误")
	}
}

func TestLearnedProfileExpiry(t *testing.T) {
	p := &LearnedProfile{
		Key:       "310260",
		Version:   1,
		ExpiresAt: time.Now().Add(-time.Hour).Unix(),
	}
	if !p.IsExpired() {
		t.Error("过期档案应报告 IsExpired=true")
	}
	p.ExpiresAt = time.Now().Add(time.Hour).Unix()
	if p.IsExpired() {
		t.Error("有效档案不应过期")
	}
}
