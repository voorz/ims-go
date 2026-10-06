package ims

import (
	"github.com/voorz/ims-go/internal/sim"
	"github.com/voorz/ims-go/internal/swu"
)

// 本文件集中 SWu 隧道的默认装配（WS-3）：
// ims.SWuConfig（公开子集）→ internal/swu.Config（内部完整配置）的单处映射（D-007）。
// 模块替换仍通过 Config.Modules.Tunnel 的 interface 注入（D-010）；
// 仅当未注入且 SWu 已配置时，New 才自动装配默认隧道。

// swuConfigured 报告 SWu 是否已配置（需要默认隧道）。
func swuConfigured(c SWuConfig) bool {
	return len(c.EPDGAddrs) > 0 || c.IMSI != ""
}

// newDefaultTunnel 由 Config 构造默认 SWu 隧道。
func newDefaultTunnel(cfg Config) (Module, error) {
	sc := &swu.Config{
		IMSI:                cfg.SWu.IMSI,
		MCC:                 cfg.SWu.MCC,
		MNC:                 cfg.SWu.MNC,
		APN:                 cfg.SWu.APN,
		LocalAddr:           cfg.SWu.LocalAddr,
		LocalPort:           cfg.SWu.LocalPort,
		AlgorithmPolicy:     cfg.SWu.AlgorithmPolicy,
		IKEProposals:        cfg.SWu.IKEProposals,
		ESPProposals:        cfg.SWu.ESPProposals,
		RekeyIKESeconds:     cfg.SWu.RekeyIKE,
		RekeyChildSeconds:   cfg.SWu.RekeyChild,
		ReauthSeconds:       cfg.SWu.Reauth,
		NATKeepaliveEvery:   cfg.SWu.NATKeepalive,
		DPDProbeEvery:       cfg.SWu.DPD,
		WiresharkKeyLogPath: cfg.SWu.WiresharkKeyLogPath,
	}
	if cfg.SWu.WiresharkKeyLogPath != "" {
		sc.EnableWiresharkKeyLog = true
	}
	if len(cfg.SWu.EPDGAddrs) > 0 {
		sc.EPDGAddr = cfg.SWu.EPDGAddrs[0]
	}
	sc.DataplaneMode = string(cfg.Dataplane.Mode)

	// AKA：优先消费方注入的硬件 provider，其次软 SIM（D-015）。
	switch {
	case cfg.SIM.AKAProvider != nil:
		sc.AKAProvider = toSimAKAProvider(cfg.SIM.AKAProvider)
	case cfg.SIM.SoftSIM.Enable:
		ss, err := sim.NewSoftSIM(cfg.SWu.IMSI, cfg.SIM.SoftSIM.Keys.inner)
		if err != nil {
			return nil, err
		}
		sc.AKAProvider = ss
	default:
		return nil, &ConfigError{
			Field:  "SIM",
			Reason: "SWu 需要 AKA：请注入 SIM.AKAProvider（硬件）或启用 SIM.SoftSIM（测试）",
		}
	}

	return swu.NewTunnel(sc), nil
}
