package swu

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// TunnelEvent 是隧道状态事件。
type TunnelEvent struct {
	State string // Session.State() 的值
	At    time.Time
}

// Tunnel 是单隧道门面（WS-3 接口收敛）：
// 把 Session 的能力收敛为 ims.Module 契约（Start/Stop）+ 隧道信息查询
// （LocalIP/PCSCFAddr/TriggerMOBIKE/Events/State）。
//
// AKA 经 Config.AKAProvider 进程内直调（internal/sim），无跨进程桥。
type Tunnel struct {
	cfg *Config

	mu      sync.Mutex
	session *Session
	events  chan TunnelEvent
}

// NewTunnel 构造单隧道门面。cfg 为 nil 时使用空配置。
// Config.OnStateChange 会被包装以同时向 Events 通道投递。
func NewTunnel(cfg *Config) *Tunnel {
	if cfg == nil {
		cfg = &Config{}
	}
	t := &Tunnel{cfg: cfg, events: make(chan TunnelEvent, 16)}
	userHook := cfg.OnStateChange
	cfg.OnStateChange = func(state string) {
		select {
		case t.events <- TunnelEvent{State: state, At: time.Now()}:
		default: // 事件通道满时丢弃，不阻塞协议机
		}
		if userHook != nil {
			userHook(state)
		}
	}
	return t
}

// Start 实现 ims.Module：建立隧道（IKE_SA_INIT → IKE_AUTH/EAP-AKA → Child SA），
// 建立后阻塞，直到 Stop/ctx 取消（返回 nil）或隧道异常退出（返回 TerminalError）。
func (t *Tunnel) Start(ctx context.Context) error {
	t.mu.Lock()
	if t.session != nil {
		t.mu.Unlock()
		return errors.New("swu: 隧道已在运行")
	}
	session := NewSession(t.cfg)
	t.session = session
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.session = nil
		t.mu.Unlock()
	}()

	if err := session.Connect(ctx); err != nil {
		return err
	}
	if werr := session.WaitDoneContext(ctx); werr != nil && ctx.Err() == nil {
		if terr := session.TerminalError(); terr != nil {
			return terr
		}
		return werr
	}
	return nil
}

// Stop 实现 ims.Module：关闭隧道，解阻塞 Start。
func (t *Tunnel) Stop() error {
	t.mu.Lock()
	s := t.session
	t.mu.Unlock()
	if s != nil {
		s.Shutdown()
	}
	return nil
}

// Close 是 Stop 的别名（满足“Close”命名习惯）。
func (t *Tunnel) Close() error { return t.Stop() }

// Events 返回隧道状态事件通道（有缓冲，满时丢弃最旧语义为丢弃新事件）。
func (t *Tunnel) Events() <-chan TunnelEvent { return t.events }

// State 返回隧道当前状态。
func (t *Tunnel) State() string {
	t.mu.Lock()
	s := t.session
	t.mu.Unlock()
	if s == nil {
		return ""
	}
	return s.State()
}

// LocalIP 返回隧道内本地地址（优先 IPv4）。
func (t *Tunnel) LocalIP() net.IP {
	t.mu.Lock()
	s := t.session
	t.mu.Unlock()
	if s == nil {
		return nil
	}
	inner := s.InnerNetwork()
	if inner.IPv4 != nil {
		return inner.IPv4
	}
	return inner.IPv6
}

// PCSCFAddr 返回协商到的 P-CSCF 地址（字符串形式，供 SIP 栈使用）。
func (t *Tunnel) PCSCFAddr() string {
	t.mu.Lock()
	s := t.session
	t.mu.Unlock()
	if s == nil {
		return ""
	}
	inner := s.InnerNetwork()
	if len(inner.PCSCF) == 0 {
		return ""
	}
	return inner.PCSCF[0].String()
}

// TriggerMOBIKE 触发 MOBIKE 地址更新。
// newLocal/newRemote 为空时从当前传输与配置自动推导。
func (t *Tunnel) TriggerMOBIKE(newLocal, newRemote string) error {
	t.mu.Lock()
	s := t.session
	t.mu.Unlock()
	if s == nil {
		return errors.New("swu: 隧道未启动")
	}
	return s.UpdateAddresses(newLocal, newRemote)
}
