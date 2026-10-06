package profile

import (
	"log/slog"
	"net/http"
	"sync"
)

// 本文件定义运营商画像（carrier profile）的类型。
// 数据源为公开的运营商画像仓库（iOS 原厂配置提取），运行时按需拉取，不嵌入二进制。
// 结构对应仓库的 bundle YAML schema（version 3）。

// Profile 是单个运营商的完整画像（对应 profiles/<slug>.yaml）。
type Profile struct {
	Version           int                      `yaml:"version"`
	Kind              string                   `yaml:"kind"`
	ID                string                   `yaml:"id"`
	Bundle            string                   `yaml:"bundle"`
	SupportedPLMNs    []string                 `yaml:"supported_plmns"`
	Common            ProfileCommon            `yaml:"common"`
	PLMNOverrides     map[string]ProfileCommon `yaml:"plmn_overrides"`
	SelectorOverrides map[string]ProfileCommon `yaml:"selector_overrides"`
}

// Effective 返回指定 PLMN 的有效配置（common + plmn_overrides 合并）。
func (p *Profile) Effective(plmn string) ProfileCommon {
	out := p.Common
	if ov, ok := p.PLMNOverrides[plmn]; ok {
		out = mergeCommon(out, ov)
	}
	return out
}

// ProfileCommon 是画像的公共配置体。
type ProfileCommon struct {
	IMS         IMSConfig         `yaml:"ims"`
	E911        E911Config        `yaml:"e911"`
	Entitlement EntitlementConfig `yaml:"entitlement"`
	Roaming     RoamingConfig     `yaml:"roaming"`
}

// IMSConfig 对应 ims 段。
type IMSConfig struct {
	APN       string          `yaml:"apn"`
	IPStack   string          `yaml:"ip_stack"`
	EPDG      EPDGConfig      `yaml:"epdg"`
	IKE       IKEConfig       `yaml:"ike"`
	Register  RegisterConfig  `yaml:"register"`
	Signaling SignalingConfig `yaml:"signaling"`
	Media     MediaConfig     `yaml:"media"`
}

// EPDGConfig 对应 ims.epdg。
type EPDGConfig struct {
	Address                 string            `yaml:"address"`
	ConfigurationAttributes []ConfigAttribute `yaml:"configuration_attributes"`
}

// ConfigAttribute 是 ePDG 配置属性（如 AssignedPCSCFIPv4）。
type ConfigAttribute struct {
	Identifier int    `yaml:"identifier"`
	Name       string `yaml:"name"`
	Type       string `yaml:"type"`
}

// IKEConfig 对应 ims.ike。
type IKEConfig struct {
	Proposals         []IKEProposal `yaml:"proposals"`
	DPD               DPDConfig     `yaml:"dpd"`
	ChildSA           ChildSAConfig `yaml:"child_sa"`
	LocalIdentifier   string        `yaml:"local_identifier"`
	SALifetimeSeconds int           `yaml:"sa_lifetime_seconds"`
}

// IKEProposal 是 IKE 提议。
type IKEProposal struct {
	Encryption           []string `yaml:"encryption"`
	Integrity            []string `yaml:"integrity"`
	PRF                  []string `yaml:"prf"`
	DHGroups             []int    `yaml:"dh_groups"`
	LifetimeSeconds      int      `yaml:"lifetime_seconds"`
	AuthenticationMethod string   `yaml:"authentication_method"`
	EAPMethod            string   `yaml:"eap_method"`
}

// DPDConfig 是 DPD 配置。
type DPDConfig struct {
	Enabled              bool `yaml:"enabled"`
	IntervalSeconds      int  `yaml:"interval_seconds"`
	MaxRetries           int  `yaml:"max_retries"`
	RetryIntervalSeconds int  `yaml:"retry_interval_seconds"`
}

// ChildSAConfig 是 Child SA 配置。
type ChildSAConfig struct {
	Proposals        []ChildSAProposal `yaml:"proposals"`
	LifetimeSeconds  int               `yaml:"lifetime_seconds"`
	ReplayWindowSize int               `yaml:"replay_window_size"`
}

// ChildSAProposal 是 Child SA 提议。
type ChildSAProposal struct {
	Encryption      []string `yaml:"encryption"`
	Integrity       []string `yaml:"integrity"`
	RekeyPFSGroup   int      `yaml:"rekey_pfs_group"`
	ESN             string   `yaml:"esn"`
	InstallPolicies bool     `yaml:"install_policies"`
	LifetimeSeconds int      `yaml:"lifetime_seconds"`
}

// RegisterConfig 对应 ims.register。
type RegisterConfig struct {
	UseIPsec                   bool `yaml:"use_ipsec"`
	IncludeCellularNetworkInfo bool `yaml:"include_cellular_network_info"`
}

// SignalingConfig 对应 ims.signaling。
type SignalingConfig struct {
	Preconditions         string        `yaml:"preconditions"`
	SupportPEarlyMedia    bool          `yaml:"support_p_early_media"`
	EarlyMediaNeedsHeader bool          `yaml:"early_media_needs_header"`
	Contact               ContactConfig `yaml:"contact"`
}

// ContactConfig 对应 signaling.contact。
type ContactConfig struct {
	AlwaysAddSIPInstance bool `yaml:"always_add_sip_instance"`
}

// MediaConfig 对应 ims.media。
type MediaConfig struct {
	RTPInactivitySeconds int `yaml:"rtp_inactivity_seconds"`
	RTCPIntervalSeconds  int `yaml:"rtcp_interval_seconds"`
}

// E911Config 对应 e911 段。
type E911Config struct {
	OverItechSupported            bool `yaml:"over_itech_supported"`
	WiFiCallingWithoutEntitlement bool `yaml:"wifi_calling_without_entitlement"`
}

// EntitlementConfig 对应 entitlement 段。
type EntitlementConfig struct {
	EntitlementURL string `yaml:"entitlement_url"`
}

// RoamingConfig 对应 roaming 段。
type RoamingConfig struct {
	WiFiCallingAllowed bool `yaml:"wifi_calling_allowed"`
}

// SelectorManifest 是 selectors/<plmn>.yaml 的结构。
type SelectorManifest struct {
	Version  int               `yaml:"version"`
	PLMN     string            `yaml:"plmn"`
	Profiles []SelectorProfile `yaml:"profiles"`
}

// SelectorProfile 是 manifest 中的单个画像条目。
type SelectorProfile struct {
	ID        string              `yaml:"id"`
	Path      string              `yaml:"path"`
	Selectors []SelectorCondition `yaml:"selectors"`
}

// SelectorCondition 是 SIM 身份匹配条件。
type SelectorCondition struct {
	Raw        string      `yaml:"raw"`
	Conditions []Condition `yaml:"conditions"`
}

// Condition 是单个匹配条件。
type Condition struct {
	Field string `yaml:"field"` // gid1/gid2/iccid/imsi
	Match string `yaml:"match"` // prefix/exact
	Value string `yaml:"value"`
}

// SIMIdentity 是用于 selector 匹配的 SIM 身份。
type SIMIdentity struct {
	IMSI  string
	ICCID string
	GID1  string
	GID2  string
	SPN   string
}

// mergeCommon 合并两个 ProfileCommon（override 优先，非零值覆盖）。
func mergeCommon(base, override ProfileCommon) ProfileCommon {
	// 简化：IMS 段整体覆盖（画像设计即按 PLMN 整体覆盖）
	if override.IMS.APN != "" || override.IMS.EPDG.Address != "" {
		base.IMS = override.IMS
	}
	if override.Entitlement.EntitlementURL != "" {
		base.Entitlement = override.Entitlement
	}
	return base
}

// Fetcher 按需拉取画像，带本地磁盘缓存。
// 流程（对应仓库 README）：
//  1. selectors/<plmn>.yaml → 匹配 SIM 身份 → bundle path
//  2. 只下载命中的 profiles/<slug>.yaml
//  3. 本地缓存优先；云端不可用时用缓存
type Fetcher struct {
	baseURL  string
	cacheDir string
	client   *http.Client
	log      *slog.Logger

	mu       sync.Mutex
	manifest map[string]*SelectorManifest // plmn → manifest（内存缓存）
	profiles map[string]*Profile          // path → profile（内存缓存）
}
