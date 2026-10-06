package inbound

import (
	"log/slog"

	"github.com/emiago/sipgo/sip"
)

// VoiceRequestHandler 是语音入站处理接口（H1：入站桥接一等能力，WS-11 实现）。
type VoiceRequestHandler interface {
	// HandleInvite 处理入站 INVITE，返回是否接管。
	HandleInvite(req *sip.Request, tx sip.ServerTransaction) bool
	// HandleBye 处理入站 BYE。
	HandleBye(req *sip.Request, tx sip.ServerTransaction) bool
}

// DialogProfile 是对话快照（供 voice 桥接）。
type DialogProfile struct {
	CallID    string
	LocalTag  string
	RemoteTag string
	RemoteURI string
}

// Dispatcher 是入站分发器。
type Dispatcher struct {
	log          *slog.Logger
	voiceHandler VoiceRequestHandler
}
