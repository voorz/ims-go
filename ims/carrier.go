package ims

import (
	"context"
	"log/slog"
	"strings"

	"github.com/voorz/ims-go/internal/carrier"
	"github.com/voorz/ims-go/internal/carrier/profile"
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

// ResolveCarrier 解析指定 PLMN 的生效运营商配置（A6）。
// 优先级：用户覆盖（Config.Carrier）> 学习 > profile > 预设 > 推导。
func (c *Client) ResolveCarrier(plmn string) (CarrierConfig, error) {
	// 用户显式配置优先
	if c.cfg.Carrier.MCCMNC != "" {
		return c.cfg.Carrier, nil
	}
	plmn = strings.TrimSpace(plmn)
	if len(plmn) < 5 {
		return CarrierConfig{}, nil
	}
	resolver := carrier.NewResolver(slog.Default(), nil)
	cc, err := resolver.ResolveEffectiveCarrierConfig(plmn[:3], plmn[3:])
	if err != nil {
		return CarrierConfig{}, err
	}
	if cc == nil {
		return CarrierConfig{}, nil
	}
	return CarrierConfig{MCCMNC: plmn}, nil
}

// FetchProfile 从云端拉取指定 PLMN 的运营商 YAML profile（A6）。
// 拉取后由主项目本地保存；不自动启用（用户手动添加流程）。
func FetchProfile(ctx context.Context, plmn string) (*CarrierProfileYAML, error) {
	fetcher := profile.NewFetcher(
		"https://raw.githubusercontent.com/voorz/ios-carrier-profiles/main",
		"",
		slog.Default(),
	)
	p, err := fetcher.FetchProfile(strings.TrimSpace(plmn), profile.SIMIdentity{})
	if err != nil {
		return nil, err
	}
	return &CarrierProfileYAML{
		Version:        p.Version,
		Kind:           p.Kind,
		ID:             p.ID,
		SupportedPLMNs: p.SupportedPLMNs,
	}, nil
}
