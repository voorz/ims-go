package ims

import (
	"errors"
	"testing"

	"github.com/voorz/ims-go/internal/sim"
)

func TestNewAutoWiresTunnel(t *testing.T) {
	cfg := Config{}
	cfg.SWu.EPDGAddrs = []string{"epdg.example.com"}
	cfg.SWu.IMSI = "001010000000001"
	cfg.SIM.SoftSIM.Enable = true
	cfg.SIM.SoftSIM.Keys = sim.TestKeys()

	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if c.cfg.Modules.Tunnel == nil {
		t.Fatal("SWu 已配置时应自动装配默认隧道")
	}
	// 未注入时不应覆盖已注入的模块
	fake := newFakeModule()
	cfg2 := Config{}
	cfg2.Modules.Tunnel = fake
	c2, err := New(cfg2)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if c2.cfg.Modules.Tunnel != Module(fake) {
		t.Error("已注入的 Tunnel 不应被默认实现覆盖")
	}
}

func TestNewTunnelRequiresAKA(t *testing.T) {
	cfg := Config{}
	cfg.SWu.EPDGAddrs = []string{"epdg.example.com"}
	_, err := New(cfg)
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("无 AKA 时应返回 *ConfigError，实际 %T: %v", err, err)
	}
}

func TestNewNoSWuNoTunnel(t *testing.T) {
	c, err := New(Config{})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if c.cfg.Modules.Tunnel != nil {
		t.Error("SWu 未配置时不应装配隧道")
	}
}
