package ims

import (
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
	return out
}
