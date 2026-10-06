package ims

import (
	"context"

	sipsdk "github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sip/inbound"
	"github.com/voorz/ims-go/internal/voice"
)

// 本文件集中语音配置的映射（WS-11）：
// ims.VoiceConfig（公开用户偏好）→ internal/voice.Config（内部完整配置）的单处映射（D-007）。
//
// 注意：语音模块当前由消费方经 Config.Modules.Voice 注入（D-010），
// 库暂不提供默认语音装配（需 SIP 栈的 Client/Server，P2 集成）。
// 本映射函数供消费方创建 voice.Agent 时使用，保证配置语义一致。

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
	from := ""
	if h := req.GetHeader("From"); h != nil {
		from = h.Value()
	}
	callID := ""
	if h := req.GetHeader("Call-ID"); h != nil {
		callID = h.Value()
	}
	return a.h.HandleIncomingCall(context.Background(), from, callID)
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
