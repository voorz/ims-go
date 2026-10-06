package ims

import (
	"context"

	sipsdk "github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sip/inbound"
	"github.com/voorz/ims-go/internal/sip/stack"
	"github.com/voorz/ims-go/internal/voice"
)

// 本文件集中语音配置的映射（WS-11）：
// ims.VoiceConfig（公开用户偏好）→ internal/voice.Config（内部完整配置）的单处映射（D-007）。
//
// A2-4：库提供默认语音装配（需 SIP 栈已装配）。消费方仍可经 Config.Modules.Voice 注入自定义实现（D-010）。

// newDefaultVoice 创建默认语音模块（A2-4）。
// 要求 cfg.Modules.SIP 已装配（取 sipgo Client/Server）；未装配时返回 nil（不装配语音）。
func newDefaultVoice(cfg Config) (Module, error) {
	// SIP 栈未装配 → 不装配语音
	if cfg.Modules.SIP == nil {
		return nil, nil
	}
	st, ok := cfg.Modules.SIP.(*stack.Stack)
	if !ok {
		// 消费方自备 SIP 实现时，自备语音模块
		return nil, nil
	}
	vc := mapVoiceConfig(cfg)
	// P-CSCF 地址：取第一个候选
	if len(cfg.SIP.PCSCFAddrs) > 0 {
		vc.PCSCFAddr = cfg.SIP.PCSCFAddrs[0]
	}
	vc.Client = st.SIPClient()
	vc.Server = st.SIPServer()
	agent := voice.NewAgent(vc)
	return &voiceModuleAdapter{agent: agent}, nil
}

// voiceModuleAdapter 将 *voice.Agent 适配为 VoiceModule 接口。
// Agent.Dial 返回 (callID string, err)，VoiceModule 需要 (*Call, error)。
type voiceModuleAdapter struct {
	agent *voice.Agent
}

func (m *voiceModuleAdapter) Start(ctx context.Context) error { return nil }

func (m *voiceModuleAdapter) Stop() error {
	m.agent.Close()
	return nil
}

func (m *voiceModuleAdapter) Dial(ctx context.Context, req CallRequest) (*Call, error) {
	callID, err := m.agent.Dial(ctx, req.To)
	if err != nil {
		return nil, err
	}
	return &Call{ID: callID, voice: m}, nil
}

func (m *voiceModuleAdapter) Hangup(ctx context.Context, callID string) error {
	return m.agent.Hangup(ctx, callID)
}

// SetMediaAddr 设置媒体地址（A2-5 的内部实现，公开经 Client.SetMediaAddr）。
func (m *voiceModuleAdapter) SetMediaAddr(localIP string, rtpPort int) {
	m.agent.SetMediaAddr(localIP, rtpPort)
}

// voiceConfigured 报告是否需要默认语音装配。
func voiceConfigured(cfg Config) bool {
	// VoiceConfig 有实质内容（非全零）或显式要求时装配
	vc := cfg.Voice
	return vc.OnIncomingCall != nil || vc.Audio != nil || len(vc.Codecs) > 0
}

// mapVoiceConfig 将公开配置映射为内部 voice.Config。
// IMPI/AKAProvider 从 SIP/SIM 配置取；LocalIP/RTPPort 需运行时设置
// （隧道 IP 是 IKEv2 完成后动态分配，静态配置时未知）。
// 调用方需另行填充 PCSCFAddr/Client/Server（由 SIP 栈提供）。
func mapVoiceConfig(cfg Config) voice.Config {
	vc := cfg.Voice
	out := voice.Config{
		IMPU:                cfg.SIP.IMPU,
		IMPI:                cfg.SIP.IMPI,
		DisableSessionTimer: vc.DisableSessionTimer,
	}
	// AKA：与 REGISTER 共用 SIM 配置（D-015）
	if cfg.SIM.AKAProvider != nil {
		out.AKAProvider = toSimAKAProvider(cfg.SIM.AKAProvider)
	}
	if len(vc.Codecs) > 0 {
		out.Codecs = vc.Codecs
	} else {
		out.Codecs = []string{"AMR-WB", "AMR", "telephone-event"}
	}
	if vc.DTMFMode != "" {
		out.DTMFMode = vc.DTMFMode
	} else {
		out.DTMFMode = "rfc4733"
	}
	if vc.MaxCalls > 0 {
		out.MaxCalls = vc.MaxCalls
	} else {
		out.MaxCalls = 2
	}
	if vc.NoAnswerTimeout > 0 {
		out.NoAnswerTimeout = vc.NoAnswerTimeout
	}
	out.Audio = toVoiceAudio(vc.Audio)
	// LocalIP/RTPPort：运行时由隧道建立后设置（见 Agent.SetMediaAddr）
	return out
}

// toVoiceAudio 将公开 AudioIO 转为内部 voice.AudioIO（nil 安全）。
func toVoiceAudio(a AudioIO) voice.AudioIO {
	if a == nil {
		return nil
	}
	return &audioAdapter{a: a}
}

type audioAdapter struct {
	a AudioIO
}

func (x *audioAdapter) ReadPCM() ([]int16, bool, error) { return x.a.ReadPCM() }
func (x *audioAdapter) WritePCM(pcm []int16) error      { return x.a.WritePCM(pcm) }
func (x *audioAdapter) SampleRate() int                 { return x.a.SampleRate() }
func (x *audioAdapter) Close() error                    { return x.a.Close() }

// incomingCallAdapter 将公开 IncomingCallHandler 适配为内部 inbound.VoiceRequestHandler。
type incomingCallAdapter struct {
	h IncomingCallHandler
}

func (a *incomingCallAdapter) HandleInvite(req *sipsdk.Request, tx sipsdk.ServerTransaction) bool {
	if a.h == nil {
		return false
	}
	icr := IncomingCallRequest{Headers: make(map[string]string)}
	if h := req.GetHeader("From"); h != nil {
		icr.From = h.Value()
	}
	if h := req.GetHeader("Call-ID"); h != nil {
		icr.CallID = h.Value()
	}
	// 关键头透传（P-Asserted-Identity / Privacy 等）
	for _, name := range []string{"P-Asserted-Identity", "Privacy", "Contact"} {
		if h := req.GetHeader(name); h != nil {
			icr.Headers[name] = h.Value()
		}
	}
	if body := req.Body(); len(body) > 0 {
		icr.RemoteSDP = string(body)
	}
	resp := a.h.HandleIncomingCall(context.Background(), icr)
	return resp.Accept
}

func (a *incomingCallAdapter) HandleBye(req *sipsdk.Request, tx sipsdk.ServerTransaction) bool {
	// BYE 由内部状态机处理，公开回调只关心来电
	return false
}

// toInboundHandler 将 VoiceConfig.OnIncomingCall 转为内部 handler（nil 安全）。
func toInboundHandler(h IncomingCallHandler) inbound.VoiceRequestHandler {
	if h == nil {
		return nil
	}
	return &incomingCallAdapter{h: h}
}
