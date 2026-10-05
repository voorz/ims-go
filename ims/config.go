package ims

import "time"

// applyDefaults 填充 Config 默认值。New 在 Validate 之前调用。
func applyDefaults(cfg Config) Config {
	if cfg.Dataplane.Mode == "" {
		cfg.Dataplane.Mode = DataplaneUserspace // D-013
	}
	if cfg.Recovery.MaxRestarts == 0 {
		cfg.Recovery.MaxRestarts = 5
	}
	if cfg.Recovery.InitialBackoff == 0 {
		cfg.Recovery.InitialBackoff = time.Second
	}
	if cfg.Recovery.MaxBackoff == 0 {
		cfg.Recovery.MaxBackoff = 30 * time.Second
	}
	return cfg
}

// Validate 对 Config 做一次集中校验（D-007），返回结构化错误。
func (c Config) Validate() error {
	switch c.Dataplane.Mode {
	case DataplaneUserspace, DataplaneTUN, DataplaneXFRMI:
	default:
		return &ConfigError{Field: "Dataplane.Mode", Reason: "非法的数据面模式"}
	}
	if c.Recovery.MaxRestarts < 0 {
		return &ConfigError{Field: "Recovery.MaxRestarts", Reason: "不能为负"}
	}
	if c.Recovery.InitialBackoff < 0 {
		return &ConfigError{Field: "Recovery.InitialBackoff", Reason: "不能为负"}
	}
	if c.Recovery.MaxBackoff < 0 {
		return &ConfigError{Field: "Recovery.MaxBackoff", Reason: "不能为负"}
	}
	if c.Recovery.MaxBackoff < c.Recovery.InitialBackoff {
		return &ConfigError{Field: "Recovery.MaxBackoff", Reason: "不能小于 InitialBackoff"}
	}
	if c.SIM.SoftSIM.Enable && !c.SIM.SoftSIM.Keys.Valid() {
		return &ConfigError{Field: "SIM.SoftSIM.Keys", Reason: "启用软 SIM 需要有效的测试密钥（sim.TestKeys / sim.CustomTestKeys）"}
	}
	return nil
}
