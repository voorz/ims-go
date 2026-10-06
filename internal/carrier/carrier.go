// Package carrier 提供运营商档案统一模型（WS-13）。
//
// 模型：preset 内嵌 + JSON 运行时覆盖 + LearnedProfileStore（P1）。
// 解析优先级：已学习 > preset > 推导（推导引擎见 WS-18）。
package carrier

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/voorz/ims-go/internal/carrier/profile"
)

// NewResolver 创建解析器。
func NewResolver(log *slog.Logger, store LearnedProfileStore) *Resolver {
	if log == nil {
		log = slog.Default()
	}
	r := &Resolver{
		log:       log,
		presets:   make(map[string]CarrierConfig),
		overrides: make(map[string]CarrierConfig),
		store:     store,
	}
	r.loadPresets()
	return r
}

// loadPresets 加载内嵌 preset。
func (r *Resolver) loadPresets() {
	// 常用运营商 preset（简化）
	presets := []CarrierConfig{
		{
			MCC: "310", MNC: "260", Name: "T-Mobile US",
			EPDG:        "epdg.epc.mnc260.mcc310.pub.3gppnetwork.org",
			IMSTemplate: IMSTemplate{Expires: 600, AccessType: "IEEE-802.11", ICSIRef: "urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel"},
		},
		{
			MCC: "234", MNC: "10", Name: "O2 UK",
			EPDG:        "epdg.epc.mnc010.mcc234.pub.3gppnetwork.org",
			IMSTemplate: IMSTemplate{Expires: 600, AccessType: "IEEE-802.11"},
		},
		{
			MCC: "460", MNC: "00", Name: "China Mobile",
			EPDG:        "epdg.epc.mnc000.mcc460.pub.3gppnetwork.org",
			IMSTemplate: IMSTemplate{Expires: 600, AccessType: "IEEE-802.11"},
		},
	}
	for _, p := range presets {
		r.presets[p.MCC+p.MNC] = p
	}
}

// SetOverride 设置 JSON 运行时覆盖。
func (r *Resolver) SetOverride(cfg CarrierConfig) {
	r.mu.Lock()
	r.overrides[cfg.MCC+cfg.MNC] = cfg
	r.mu.Unlock()
}

// LoadOverrideJSON 从 JSON 加载覆盖。
func (r *Resolver) LoadOverrideJSON(data []byte) error {
	var cfg CarrierConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("carrier: JSON 解析失败: %w", err)
	}
	r.SetOverride(cfg)
	return nil
}

// ResolveEffectiveCarrierConfig 解析有效配置（单函数，优先级：已学习 > preset > 推导）。
func (r *Resolver) ResolveEffectiveCarrierConfig(mcc, mnc string) (*CarrierConfig, error) {
	key := mcc + mnc

	// 1. JSON 覆盖（用户手动指定，最高优先级）
	r.mu.RLock()
	if cfg, ok := r.overrides[key]; ok {
		r.mu.RUnlock()
		r.log.Info("使用 JSON 覆盖的运营商配置", "mcc", mcc, "mnc", mnc)
		return &cfg, nil
	}
	r.mu.RUnlock()

	// 2. 已学习（P1）
	if r.store != nil {
		if profile, err := r.store.Load(key); err == nil && profile != nil {
			r.log.Info("使用已学习的运营商配置", "mcc", mcc, "mnc", mnc, "version", profile.Version)
			return &profile.Config, nil
		}
	}

	// 3. 运营商画像（按需拉取，iOS 原厂数据）
	if r.profileFetcher != nil {
		plmn := mcc + mnc // 注意：保持真实 MNC 宽度，不强制补零
		var sim profile.SIMIdentity
		if r.simIdentityProvider != nil {
			sim = r.simIdentityProvider()
		}
		if p, err := r.profileFetcher.FetchProfile(plmn, sim); err == nil && p != nil {
			r.log.Info("使用运营商画像", "mcc", mcc, "mnc", mnc, "profile", p.ID)
			return profileToConfig(p, plmn, mcc, mnc), nil
		} else if err != nil {
			r.log.Warn("画像拉取失败，回退 preset", "err", err)
		}
	}

	// 4. 内嵌 preset
	r.mu.RLock()
	if cfg, ok := r.presets[key]; ok {
		r.mu.RUnlock()
		return &cfg, nil
	}
	r.mu.RUnlock()

	// 5. 推导（默认模板，WS-18 完善推导引擎）
	r.log.Info("未找到运营商档案，使用推导默认", "mcc", mcc, "mnc", mnc)
	return &CarrierConfig{
		MCC:  mcc,
		MNC:  mnc,
		Name: "Derived",
		EPDG: fmt.Sprintf("epdg.epc.mnc%s.mcc%s.pub.3gppnetwork.org", mnc, mcc),
		IMSTemplate: IMSTemplate{
			Expires:    600,
			AccessType: "IEEE-802.11",
		},
	}, nil
}

// SetProfileFetcher 设置画像拉取器（可选）。
// 启用后，画像作为 preset 层的数据源（优先级：覆盖 > 已学习 > 画像 > 内嵌 preset > 推导）。
func (r *Resolver) SetProfileFetcher(f *profile.Fetcher, simProvider func() profile.SIMIdentity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.profileFetcher = f
	r.simIdentityProvider = simProvider
}

// IsVoWiFiBlockedMCC 报告 MCC 是否被阻止 VoWiFi。
func (r *Resolver) IsVoWiFiBlockedMCC(mcc string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, cfg := range r.presets {
		if cfg.MCC == mcc && cfg.VoWiFiBlocked {
			return true
		}
	}
	for _, cfg := range r.overrides {
		if cfg.MCC == mcc && cfg.VoWiFiBlocked {
			return true
		}
	}
	return false
}
