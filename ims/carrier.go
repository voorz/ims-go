package ims

import (
	"strings"

	"github.com/voorz/ims-go/internal/carrier"
)

// 本文件集中运营商配置的映射（WS-13）：
// ims.CarrierConfig（公开覆盖层）→ internal/carrier.CarrierConfig（内部完整模型）的单处映射（D-007）。
//
// 优先级：ims.CarrierConfig 字段 > LearnedProfile > preset > 推导。

// mapCarrierConfig 将公开 CarrierConfig 映射为内部 override。
// 返回 nil 表示无覆盖（全部走自动流程）。
func mapCarrierConfig(cfg CarrierConfig) *carrier.CarrierConfig {
	if cfg.MCCMNC == "" && cfg.EPDGAddr == "" {
		return nil
	}
	out := &carrier.CarrierConfig{}
	if cfg.MCCMNC != "" {
		// MCCMNC 如 "23415"：前 3 位 MCC，余下 MNC
		mccmnc := strings.TrimSpace(cfg.MCCMNC)
		if len(mccmnc) >= 5 {
			out.MCC = mccmnc[:3]
			out.MNC = mccmnc[3:]
		}
	}
	if cfg.EPDGAddr != "" {
		out.EPDG = cfg.EPDGAddr
	}
	return out
}
