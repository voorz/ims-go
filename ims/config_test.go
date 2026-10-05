package ims

import (
	"errors"
	"testing"
	"time"
)

func TestConfig_Validate_AppliesDefaults(t *testing.T) {
	c, err := New(Config{})
	if err != nil {
		t.Fatalf("New 空配置不应失败: %v", err)
	}
	if c.cfg.Dataplane.Mode != DataplaneUserspace {
		t.Errorf("数据面默认模式应为 userspace，实际 %q", c.cfg.Dataplane.Mode)
	}
	if c.cfg.Recovery.MaxRestarts != 5 {
		t.Errorf("默认 MaxRestarts 应为 5，实际 %d", c.cfg.Recovery.MaxRestarts)
	}
	if c.cfg.Recovery.InitialBackoff != time.Second {
		t.Errorf("默认 InitialBackoff 应为 1s，实际 %v", c.cfg.Recovery.InitialBackoff)
	}
}

func TestConfig_Validate_BadDataplaneMode(t *testing.T) {
	_, err := New(Config{Dataplane: DataplaneConfig{Mode: "wifi"}})
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("应返回 *ConfigError，实际 %T: %v", err, err)
	}
	if ce.Field != "Dataplane.Mode" {
		t.Errorf("Field 应为 Dataplane.Mode，实际 %q", ce.Field)
	}
}

func TestConfig_Validate_NegativeBackoff(t *testing.T) {
	cfg := Config{}
	cfg.Recovery.InitialBackoff = -time.Second
	_, err := New(cfg)
	if err == nil {
		t.Fatal("负退避应校验失败")
	}
}

func TestConfig_Validate_MaxBackoffLessThanInitial(t *testing.T) {
	cfg := Config{}
	cfg.Recovery.InitialBackoff = 10 * time.Second
	cfg.Recovery.MaxBackoff = time.Second
	_, err := New(cfg)
	if err == nil {
		t.Fatal("MaxBackoff < InitialBackoff 应校验失败")
	}
}
