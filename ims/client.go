package ims

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/voorz/ims-go/internal/carrier"
	"github.com/voorz/ims-go/internal/identity"
)

// New 构造客户端：填充默认值 → 一次集中校验（D-007）→ PrepareStart（A4）→ 默认模块装配。
// 子系统通过 Config.Modules 以 interface 注入（D-010）；
// 为 nil 的槽位表示该能力未装配，调用对应方法时返回哨兵错误。
// 例外：SWu 隧道在已配置但未注入时自动装配默认实现（见 ims/swu.go）。
func New(cfg Config) (*Client, error) {
	cfg = applyDefaults(cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	// A4：PrepareStart——Profile 校验 → 运营商解析 → 身份三态。
	// MCC/MNC 为空时跳过（消费者可能后续设置）。
	var ident *identity.Identity
	if cfg.SWu.MCC != "" && cfg.SWu.MNC != "" {
		resolver := carrier.NewResolver(slog.Default(), nil)
		var carrierCfg *carrier.CarrierConfig
		var err error
		ident, carrierCfg, err = identity.PrepareStart(slog.Default(), identity.PrepareInput{
			MCC:      cfg.SWu.MCC,
			MNC:      cfg.SWu.MNC,
			IMPI:     cfg.SIP.IMPI,
			IMPU:     cfg.SIP.IMPU,
			Mode:     identity.ModeAuto,
			Resolver: resolver,
		})
		if err != nil {
			return nil, err
		}
		// 运营商解析结果回填：用户显式配置优先（CarrierConfig 非空即覆盖）。
		if carrierCfg != nil {
			applyCarrierConfig(&cfg, carrierCfg)
		}
		// 身份回填：推导出的 IMPI/IMPU 写回（ISIM 模式已在输入中）。
		if ident != nil {
			if cfg.SIP.IMPI == "" {
				cfg.SIP.IMPI = ident.IMPI
			}
			if cfg.SIP.IMPU == "" {
				cfg.SIP.IMPU = ident.IMPU
			}
		}
	}
	if cfg.Modules.Tunnel == nil && swuConfigured(cfg.SWu) {
		tunnel, err := newDefaultTunnel(cfg)
		if err != nil {
			return nil, err
		}
		cfg.Modules.Tunnel = tunnel
		slog.Info("ims: SWu 隧道模块已装配", "epdg_addrs", cfg.SWu.EPDGAddrs, "imsi_set", cfg.SWu.IMSI != "")
	} else if cfg.Modules.Tunnel == nil {
		slog.Warn("ims: SWu 隧道模块未装配（EPDGAddrs 为空且 IMSI 为空），隧道不会建立",
			"epdg_addrs", cfg.SWu.EPDGAddrs, "imsi_set", cfg.SWu.IMSI != "", "mcc", cfg.SWu.MCC, "mnc", cfg.SWu.MNC)
	}
	if cfg.Modules.SIP == nil && sipConfigured(cfg.SIP) {
		sipMod, err := newDefaultSIP(cfg)
		if err != nil {
			return nil, err
		}
		cfg.Modules.SIP = sipMod
		slog.Info("ims: SIP 模块已装配", "pcscf_addrs", cfg.SIP.PCSCFAddrs, "impu_set", cfg.SIP.IMPU != "")
	} else if cfg.Modules.SIP == nil {
		slog.Warn("ims: SIP 模块未装配（PCSCFAddrs 为空且 IMPU 为空），IMS 注册不会进行",
			"pcscf_addrs", cfg.SIP.PCSCFAddrs, "impu_set", cfg.SIP.IMPU != "")
	}
	// SMS 默认装配（需 SIP 栈；Store 可选）。
	if cfg.Modules.SMS == nil && cfg.Modules.SIP != nil {
		smsMod, err := newDefaultSMS(cfg, cfg.Modules.SIP)
		if err == nil {
			cfg.Modules.SMS = smsMod
		}
		// 非 stack.Stack 时跳过（消费者自备 SIP 实现时自备 SMS）
	}
	// USSD 默认装配（需 SIP 栈）。
	if cfg.Modules.USSD == nil && cfg.Modules.SIP != nil {
		ussdMod, err := newDefaultUSSD(cfg, cfg.Modules.SIP)
		if err == nil {
			cfg.Modules.USSD = ussdMod
		}
	}
	// Voice 默认装配（A2-4，需 SIP 栈；消费方可注入自定义实现）。
	if cfg.Modules.Voice == nil && voiceConfigured(cfg) {
		voiceMod, err := newDefaultVoice(cfg)
		if err == nil && voiceMod != nil {
			cfg.Modules.Voice = voiceMod.(VoiceModule)
		}
	}
	c := &Client{
		cfg:      cfg,
		disp:     newDispatcher(),
		modState: make(map[string]bool),
		identity: ident,
	}
	c.state.Store(int32(lcStopped))
	return c, nil
}

// applyCarrierConfig 将内部运营商解析结果回填到公开 Config。
// 用户显式配置（cfg.Carrier 非零值）优先，不被覆盖。
func applyCarrierConfig(cfg *Config, cc *carrier.CarrierConfig) {
	// 仅回填用户未显式设置的字段；具体字段映射按 carrier.CarrierConfig 结构。
	// 当前为占位：后续按实际字段细化。
	_ = cfg
	_ = cc
}

// slots 按固定顺序返回已装配的模块槽位：tunnel → sip → sms → ussd → voice。
func (c *Client) slots() []moduleSlot {
	m := c.cfg.Modules
	var out []moduleSlot
	if m.Tunnel != nil {
		out = append(out, moduleSlot{name: "tunnel", module: m.Tunnel})
	}
	if m.SIP != nil {
		out = append(out, moduleSlot{name: "sip", module: m.SIP})
	}
	if m.SMS != nil {
		out = append(out, moduleSlot{name: "sms", module: m.SMS})
	}
	if m.USSD != nil {
		out = append(out, moduleSlot{name: "ussd", module: m.USSD})
	}
	if m.Voice != nil {
		out = append(out, moduleSlot{name: "voice", module: m.Voice})
	}
	return out
}

func (c *Client) getCtx() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ctx
}

// Start 启动客户端：状态机 stopped→starting→running（CAS 防并发启动，H2）。
// 各模块在独立监督 goroutine 中运行（H3 恢复策略）。
func (c *Client) Start(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !c.state.CompareAndSwap(int32(lcStopped), int32(lcStarting)) {
		return ErrAlreadyRunning
	}

	cctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.ctx = cctx
	c.cancel = cancel
	c.startedAt = time.Now()
	c.mu.Unlock()

	slots := c.slots()
	slog.Info("ims: 客户端启动", "modules", len(slots))
	for i, s := range slots {
		slog.Info("ims: 启动模块", "index", i, "name", s.name)
	}
	for _, s := range slots {
		c.wg.Add(1)
		go c.supervise(s)
	}

	c.state.Store(int32(lcRunning))
	c.disp.publish(Event{Type: EventClientStarted, Reason: "客户端启动完成"})
	return nil
}

// Stop 停止客户端：running→stopping→stopped；幂等。
func (c *Client) Stop() error {
	if c.state.Load() == int32(lcStopped) {
		return nil
	}
	if !c.state.CompareAndSwap(int32(lcRunning), int32(lcStopping)) {
		return ErrNotRunning
	}

	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// 通知各模块 Stop，解阻塞其 Start。
	for _, s := range c.slots() {
		_ = s.module.Stop()
	}
	c.wg.Wait()

	c.state.Store(int32(lcStopped))
	c.disp.publish(Event{Type: EventClientStopped, Reason: "客户端已停止"})
	c.disp.shutdown()
	return nil
}

// IsRunning 报告客户端是否处于运行态（已 Start 且未 Stop）。
// 用于调用方检测僵尸实例：非 nil 但已停止的 Client 应被视为不存在。
func (c *Client) IsRunning() bool {
	if c == nil {
		return false
	}
	return c.state.Load() == int32(lcRunning)
}

// supervise 监督单个模块：非预期退出时按 RecoveryPolicy 退避重启（H3），
// 每次决策 emit 结构化决策记录（P2）。
func (c *Client) supervise(s moduleSlot) {
	defer c.wg.Done()

	pol := c.cfg.Recovery
	backoff := pol.InitialBackoff
	restarts := 0

	c.setModuleState(s.name, true)
	c.disp.publish(Event{Type: EventModuleStarted, Module: s.name})

run:
	for {
		err := s.module.Start(c.getCtx())
		if c.getCtx().Err() != nil {
			break run // 客户端正在关闭：正常退出，不重启
		}

		reason := "模块非预期退出"
		if err != nil {
			reason = err.Error()
		}
		restarts++

		chosen := "restart"
		if pol.Disabled || restarts > pol.MaxRestarts {
			chosen = "give-up"
		}
		c.RecordDecision(DecisionRecord{
			Point:  "module-recovery",
			Inputs: []string{"module=" + s.name, "restarts=" + strconv.Itoa(restarts)},
			Chosen: chosen,
			Reason: reason,
		})

		if chosen == "give-up" {
			c.disp.publish(Event{
				Type:   EventModuleFailed,
				Module: s.name,
				Reason: "超过最大重启次数，放弃重启：" + reason,
			})
			break run
		}

		c.disp.publish(Event{Type: EventModuleFailed, Module: s.name, Reason: reason})

		select {
		case <-c.getCtx().Done():
			break run
		case <-time.After(backoff):
		}
		c.disp.publish(Event{
			Type:   EventModuleRestarted,
			Module: s.name,
			Reason: "第 " + strconv.Itoa(restarts) + " 次重启",
		})
		backoff *= 2
		if backoff > pol.MaxBackoff {
			backoff = pol.MaxBackoff
		}
	}

	c.setModuleState(s.name, false)
	c.disp.publish(Event{Type: EventModuleStopped, Module: s.name})
}

func (c *Client) setModuleState(name string, running bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.modState[name] = running
}

// Status 返回客户端状态快照（强类型，H7）。
func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := Status{
		State:     lcState(c.state.Load()).public(),
		StartedAt: c.startedAt,
	}
	for _, s := range c.slots() {
		st.Modules = append(st.Modules, ModuleStatus{Name: s.name, Running: c.modState[s.name]})
	}
	return st
}

// OnEvent 订阅事件（H5：单一事件通道），返回取消订阅函数。
func (c *Client) OnEvent(h EventHandler) (unsubscribe func()) {
	return c.disp.subscribe(h)
}

// RecordDecision 记录一条结构化决策记录（P2），保留最近 maxDecisionRecords 条。
func (c *Client) RecordDecision(r DecisionRecord) {
	if r.At.IsZero() {
		r.At = time.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.decisions = append(c.decisions, r)
	if len(c.decisions) > maxDecisionRecords {
		c.decisions = c.decisions[len(c.decisions)-maxDecisionRecords:]
	}
}

// Decisions 查询决策记录（P2：不再靠日志考古）。
func (c *Client) Decisions() []DecisionRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]DecisionRecord, len(c.decisions))
	copy(out, c.decisions)
	return out
}

// SendSMS 发送短信。SMS 模块未装配时返回 ErrNoSMSModule。
func (c *Client) SendSMS(ctx context.Context, req SMSRequest) (*SMSResult, error) {
	if c.state.Load() != int32(lcRunning) {
		return nil, ErrNotRunning
	}
	m := c.cfg.Modules.SMS
	if m == nil {
		return nil, ErrNoSMSModule
	}
	return m.Send(ctx, req)
}

// SendUSSD 发送 USSD 请求。USSD 模块未装配时返回 ErrNoUSSDModule。
func (c *Client) SendUSSD(ctx context.Context, code string) (*USSDResult, error) {
	if c.state.Load() != int32(lcRunning) {
		return nil, ErrNotRunning
	}
	m := c.cfg.Modules.USSD
	if m == nil {
		return nil, ErrNoUSSDModule
	}
	return m.Send(ctx, code)
}

// ContinueUSSD 回复当前 USSD 会话（菜单交互）。
func (c *Client) ContinueUSSD(ctx context.Context, input string) (*USSDResult, error) {
	if c.state.Load() != int32(lcRunning) {
		return nil, ErrNotRunning
	}
	m := c.cfg.Modules.USSD
	if m == nil {
		return nil, ErrNoUSSDModule
	}
	return m.Continue(ctx, input)
}

// CancelUSSD 取消当前 USSD 会话。
func (c *Client) CancelUSSD(ctx context.Context) error {
	if c.state.Load() != int32(lcRunning) {
		return ErrNotRunning
	}
	m := c.cfg.Modules.USSD
	if m == nil {
		return ErrNoUSSDModule
	}
	return m.Cancel(ctx)
}

// Voice 返回语音网关访问器。
func (c *Client) Voice() VoiceControl {
	return VoiceControl{m: c.cfg.Modules.Voice}
}

// Dial 发起语音呼叫。
func (v VoiceControl) Dial(ctx context.Context, req CallRequest) (*Call, error) {
	if v.m == nil {
		return nil, ErrNoVoiceModule
	}
	call, err := v.m.Dial(ctx, req)
	if err != nil {
		return nil, err
	}
	call.voice = v.m
	return call, nil
}

// Hangup 挂断呼叫。
func (c *Call) Hangup(ctx context.Context) error {
	if c.voice == nil {
		return ErrNoVoiceModule
	}
	return c.voice.Hangup(ctx, c.ID)
}

// SetMediaAddr 设置语音媒体地址（A2-5）。
// localIP 是隧道内 IP（IKEv2 完成后分配），rtpPort 是 RTP 端口。
// 需在隧道建立后、呼叫前调用；SDP 中的媒体地址据此生成。
func (v VoiceControl) SetMediaAddr(localIP string, rtpPort int) error {
	if v.m == nil {
		return ErrNoVoiceModule
	}
	// 仅默认装配的 voiceModuleAdapter 支持；自定义实现可忽略
	if a, ok := v.m.(*voiceModuleAdapter); ok {
		a.SetMediaAddr(localIP, rtpPort)
		return nil
	}
	return nil
}
