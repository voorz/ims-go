package ims

import (
	"context"
	"net"

	"github.com/voorz/ims-go/internal/sip/register"
	"github.com/voorz/ims-go/internal/sip/stack"
	"github.com/voorz/ims-go/internal/sip/transport"
)

// 本文件集中 SIP 协议栈的默认装配（WS-6/10 集成）：
// ims.SIPConfig（公开子集）→ internal/sip/stack.Config（内部完整配置）的单处映射（D-007）。
// 模块替换仍通过 Config.Modules.SIP 的 interface 注入（D-010）；
// 仅当未注入且 SIP 已配置时，New 才自动装配默认协议栈。

// sipConfigured 报告 SIP 是否已配置（需要默认协议栈）。
func sipConfigured(c SIPConfig) bool {
	return len(c.PCSCFAddrs) > 0 || c.IMPU != ""
}

// newDefaultSIP 由 Config 构造默认 SIP 协议栈。
func newDefaultSIP(cfg Config) (Module, error) {
	sc := stack.Config{
		IMPU:             cfg.SIP.IMPU,
		IMPI:             cfg.SIP.IMPI,
		HomeDomain:       cfg.SIP.HomeDomain,
		PCSCFAddrs:       cfg.SIP.PCSCFAddrs,
		Contact:          cfg.SIP.Contact,
		RegisterExpires:  cfg.SIP.RegisterExpires,
		SubscribeExpires: cfg.SIP.SubscribeExpires,
		EAPRES:           cfg.SIP.EAPRES,
		VoiceHandler:     toInboundHandler(cfg.Voice.OnIncomingCall),
		Dialer:           toTransportDialer(cfg.SIP.Dialer),
		VariantStore:     toRegisterVariantStore(cfg.SIP.VariantStore),
	}
	// AKA：与 SWu 共用（D-015）。
	switch {
	case cfg.SIM.AKAProvider != nil:
		sc.AKAProvider = toSimAKAProvider(cfg.SIM.AKAProvider)
	default:
		// 无 AKA 时仍可装配（REGISTER 会 401 失败，由调用方处理）。
	}
	return stack.New(sc)
}

// toTransportDialer 将公开 Dialer 转为内部 transport.Dialer（nil 安全）。
func toTransportDialer(d Dialer) transport.Dialer {
	if d == nil {
		return nil
	}
	return &dialerAdapter{d: d}
}

type dialerAdapter struct {
	d Dialer
}

func (a *dialerAdapter) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return a.d.DialContext(ctx, network, address)
}

// toRegisterVariantStore 将公开 VariantStore 转为内部 register.VariantStore（nil 安全）。
func toRegisterVariantStore(s VariantStore) register.VariantStore {
	if s == nil {
		return nil
	}
	return &variantStoreAdapter{s: s}
}

type variantStoreAdapter struct {
	s VariantStore
}

func (a *variantStoreAdapter) LoadVariant(impu string) (string, error) {
	return a.s.LoadVariant(impu)
}

func (a *variantStoreAdapter) SaveVariant(impu, variant string) error {
	return a.s.SaveVariant(impu, variant)
}
