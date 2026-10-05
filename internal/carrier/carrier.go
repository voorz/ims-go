// Package carrier 提供运营商档案统一模型（WS-13）。
//
// 模型：preset 内嵌 + JSON 运行时覆盖 + LearnedProfileStore（P1）。
// 解析优先级：已学习 > preset > 推导（推导引擎见 WS-18）。
package carrier

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
)

// CarrierConfig 是统一运营商档案。
type CarrierConfig struct {
	MCC  string `json:"mcc"`
	MNC  string `json:"mnc"`
	Name string `json:"name"`

	// EPDG 是 ePDG 地址（普通流程永不选紧急 ePDG）。
	EPDG string `json:"epdg"`

	// VoWiFiBlocked 标记该 MCC 是否被阻止 VoWiFi。
	VoWiFiBlocked bool `json:"vowifi_blocked"`

	// IMS 注册模板
	IMSTemplate IMSTemplate `json:"ims_template"`

	// E911 策略
	E911 E911Policy `json:"e911"`
}

// IMSTemplate 是 IMS 注册模板。
type IMSTemplate struct {
	Expires              int    `json:"expires"`
	ContactMode          string `json:"contact_mode"`
	AccessType           string `json:"access_type"`
	ICSIRef              string `json:"icsi_ref"`
	UseDigestPlaceholder bool   `json:"use_digest_placeholder"`
}

// E911Policy 是紧急地址策略。
type E911Policy struct {
	Enabled        bool   `json:"enabled"`
	Provider       string `json:"provider"`
	EntitlementURL string `json:"entitlement_url"`
}

// LearnedProfile 是已学习的运营商档案（P1）。
type LearnedProfile struct {
	Key       string        `json:"key"` // MCC/MNC+SPN/GID
	Config    CarrierConfig `json:"config"`
	Version   int           `json:"version"`
	ExpiresAt int64         `json:"expires_at"` // unix 时间
}

// LearnedProfileStore 是 learned-profile 存储接口（消费方实现持久化）。
type LearnedProfileStore interface {
	// Load 加载已学习的档案；不存在返回 (nil, nil)。
	Load(key string) (*LearnedProfile, error)
	// Save 保存已学习的档案。
	Save(profile *LearnedProfile) error
}

// Resolver 解析有效运营商配置。
type Resolver struct {
	log       *slog.Logger
	mu        sync.RWMutex
	presets   map[string]CarrierConfig // key: MCC+MNC
	overrides map[string]CarrierConfig
	store     LearnedProfileStore
}

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

	// 1. JSON 覆盖
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

	// 3. preset
	r.mu.RLock()
	if cfg, ok := r.presets[key]; ok {
		r.mu.RUnlock()
		return &cfg, nil
	}
	r.mu.RUnlock()

	// 4. 推导（默认模板，WS-18 完善推导引擎）
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
