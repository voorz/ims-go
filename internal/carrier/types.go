package carrier

import (
	"log/slog"
	"sync"
	"time"
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

// DeriveParam 是可推导的参数。
type DeriveParam string

// ParamVariant 是一组参数变体。
type ParamVariant struct {
	Name   string
	Params map[DeriveParam]string
}

// ProbeResult 是试探结果。
type ProbeResult struct {
	Variant ParamVariant
	Success bool
	Detail  string
	At      time.Time
}

// DeriveEngine 是推导引擎（P1：试探→收敛→持久化）。
type DeriveEngine struct {
	log      *slog.Logger
	resolver *Resolver
	// OnProbe 是试探回调（实际执行 REGISTER 等）。
	OnProbe func(variant ParamVariant) ProbeResult
	// OnDecision 决策记录（P2 复用）。
	OnDecision func(decision string, detail string)
}
