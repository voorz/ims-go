package carrier

import (
	"fmt"
	"log/slog"
	"time"
)

const (
	ParamRegisterTemplate  DeriveParam = "register_template"
	ParamSecurityMechanism DeriveParam = "security_mechanism"
	ParamPCSCFStrategy     DeriveParam = "pcscf_strategy"
	ParamKeepaliveInterval DeriveParam = "keepalive_interval"
	ParamSMSRetry          DeriveParam = "sms_retry"
)

// NewDeriveEngine 创建推导引擎。
func NewDeriveEngine(log *slog.Logger, resolver *Resolver) *DeriveEngine {
	if log == nil {
		log = slog.Default()
	}
	return &DeriveEngine{log: log, resolver: resolver}
}

// Variants 返回试探变体（按优先级排序）。
func (e *DeriveEngine) Variants() []ParamVariant {
	return []ParamVariant{
		{
			Name: "default",
			Params: map[DeriveParam]string{
				ParamRegisterTemplate:  "standard",
				ParamSecurityMechanism: "ipsec-3gpp",
				ParamPCSCFStrategy:     "dns-first",
				ParamKeepaliveInterval: "30s",
				ParamSMSRetry:          "3",
			},
		},
		{
			Name: "no-ipsec",
			Params: map[DeriveParam]string{
				ParamRegisterTemplate:  "standard",
				ParamSecurityMechanism: "none",
				ParamPCSCFStrategy:     "dns-first",
				ParamKeepaliveInterval: "30s",
				ParamSMSRetry:          "3",
			},
		},
		{
			Name: "aggressive-keepalive",
			Params: map[DeriveParam]string{
				ParamRegisterTemplate:  "standard",
				ParamSecurityMechanism: "ipsec-3gpp",
				ParamPCSCFStrategy:     "dns-first",
				ParamKeepaliveInterval: "15s",
				ParamSMSRetry:          "5",
			},
		},
	}
}

// Derive 执行推导：试探变体直到成功或放弃。
// 成功信号：REGISTER 200 + 稳定时长（由 OnProbe 判定）。
func (e *DeriveEngine) Derive(mcc, mnc string) (*CarrierConfig, error) {
	if e.OnProbe == nil {
		return nil, fmt.Errorf("carrier: 推导需要 OnProbe 回调")
	}
	key := mcc + mnc
	var tried []string
	for _, variant := range e.Variants() {
		tried = append(tried, variant.Name)
		e.log.Info("试探参数变体", "variant", variant.Name, "mcc", mcc, "mnc", mnc)
		result := e.OnProbe(variant)
		if e.OnDecision != nil {
			e.OnDecision("derive-probe", fmt.Sprintf("variant=%s success=%v %s", variant.Name, result.Success, result.Detail))
		}
		if result.Success {
			e.log.Info("推导收敛", "variant", variant.Name)
			cfg := e.variantToConfig(mcc, mnc, variant)
			// 持久化已学习档案
			if e.resolver.store != nil {
				profile := &LearnedProfile{
					Key:       key,
					Config:    *cfg,
					Version:   1,
					ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Unix(),
				}
				if err := e.resolver.store.Save(profile); err != nil {
					e.log.Warn("保存已学习档案失败", "error", err)
				}
			}
			if e.OnDecision != nil {
				e.OnDecision("derive-converged", fmt.Sprintf("variant=%s tried=%v", variant.Name, tried))
			}
			return cfg, nil
		}
	}
	if e.OnDecision != nil {
		e.OnDecision("derive-gave-up", fmt.Sprintf("tried=%v", tried))
	}
	return nil, fmt.Errorf("carrier: 推导放弃，已试 %v", tried)
}

// variantToConfig 将变体转为 CarrierConfig。
func (e *DeriveEngine) variantToConfig(mcc, mnc string, variant ParamVariant) *CarrierConfig {
	cfg, _ := e.resolver.ResolveEffectiveCarrierConfig(mcc, mnc)
	// 应用变体参数（简化：记录变体名）
	cfg.Name = cfg.Name + " (derived:" + variant.Name + ")"
	return cfg
}

// IsExpired 报告已学习档案是否失效。
func (p *LearnedProfile) IsExpired() bool {
	return time.Now().Unix() > p.ExpiresAt
}
