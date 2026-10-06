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

// mapVoiceConfig 将公开 VoiceConfig 映射为内部 voice.Config 的偏好部分。
// 调用方需另行填充 IMPU/PCSCFAddr/Client/Server（由 SIP 栈提供）。
func mapVoiceConfig(cfg VoiceConfig) voice.Config {
	out := voice.Config{
		DisableSessionTimer: cfg.DisableSessionTimer,
	}
	if len(cfg.Codecs) > 0 {
		out.Codecs = cfg.Codecs
	} else {
		out.Codecs = []string{"AMR-WB", "AMR", "telephone-event"}
	}
	if cfg.DTMFMode != "" {
		out.DTMFMode = cfg.DTMFMode
	} else {
		out.DTMFMode = "rfc4733"
	}
	if cfg.MaxCalls > 0 {
		out.MaxCalls = cfg.MaxCalls
	} else {
		out.MaxCalls = 2
	}
	if cfg.NoAnswerTimeout > 0 {
		out.NoAnswerTimeout = cfg.NoAnswerTimeout
	}
	out.Audio = toVoiceAudio(cfg.Audio)
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
