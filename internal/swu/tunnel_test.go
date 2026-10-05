package swu

import (
	"testing"
	"time"

	"context"
)

func TestNewTunnelNilConfig(t *testing.T) {
	tun := NewTunnel(nil)
	if tun == nil {
		t.Fatal("NewTunnel(nil) 不应返回 nil")
	}
	if got := tun.State(); got != "" {
		t.Errorf("未启动时 State 应为空，实际 %q", got)
	}
	if tun.LocalIP() != nil {
		t.Error("未启动时 LocalIP 应为 nil")
	}
	if tun.PCSCFAddr() != "" {
		t.Error("未启动时 PCSCFAddr 应为空")
	}
	if err := tun.Stop(); err != nil {
		t.Errorf("未启动时 Stop 应返回 nil，实际 %v", err)
	}
	if err := tun.TriggerMOBIKE("", ""); err == nil {
		t.Error("未启动时 TriggerMOBIKE 应返回错误")
	}
}

func TestTunnelEventsChannel(t *testing.T) {
	tun := NewTunnel(&Config{})
	select {
	case <-tun.Events():
		t.Error("不应有事件")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTunnelDoubleStartGuard(t *testing.T) {
	tun := NewTunnel(nil)
	// 同包测试：直接设置 session 模拟运行中状态
	tun.mu.Lock()
	tun.session = NewSession(&Config{})
	tun.mu.Unlock()
	if err := tun.Start(context.Background()); err == nil {
		t.Error("隧道已在运行时 Start 应返回错误")
	}
}

func TestTunnelStopIdempotent(t *testing.T) {
	tun := NewTunnel(&Config{EPDGAddr: "192.0.2.1"})
	if err := tun.Stop(); err != nil {
		t.Errorf("Stop 应幂等，实际 %v", err)
	}
	if err := tun.Close(); err != nil {
		t.Errorf("Close 应幂等，实际 %v", err)
	}
}
