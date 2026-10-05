// Package inbound 提供入站 SIP 请求分发（WS-10）。
//
// 覆盖：INVITE/BYE/CANCEL/OPTIONS/NOTIFY/MESSAGE 路由；
// 单级事件分发；VoiceRequestHandler 接口（WS-11 桥接契约，H1）。
package inbound

import (
	"log/slog"

	"github.com/emiago/sipgo"
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

// New 创建分发器。
func New(log *slog.Logger, voiceHandler VoiceRequestHandler) *Dispatcher {
	if log == nil {
		log = slog.Default()
	}
	return &Dispatcher{log: log, voiceHandler: voiceHandler}
}

// Register 在 server 上注册所有入站处理器。
func (d *Dispatcher) Register(server *sipgo.Server) {
	server.OnRequest(sip.INVITE, d.handleInvite)
	server.OnRequest(sip.BYE, d.handleBye)
	server.OnRequest(sip.CANCEL, d.handleCancel)
	server.OnRequest(sip.OPTIONS, d.handleOptions)
	// NOTIFY/MESSAGE 由 subscribe/sms 包各自注册
}

func (d *Dispatcher) handleInvite(req *sip.Request, tx sip.ServerTransaction) {
	if d.voiceHandler != nil && d.voiceHandler.HandleInvite(req, tx) {
		return
	}
	// 无语音处理器：回 486 Busy
	d.log.Info("入站 INVITE 无处理器，回 486")
	res := sip.NewResponseFromRequest(req, 486, "Busy Here", nil)
	_ = tx.Respond(res)
}

func (d *Dispatcher) handleBye(req *sip.Request, tx sip.ServerTransaction) {
	if d.voiceHandler != nil && d.voiceHandler.HandleBye(req, tx) {
		return
	}
	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	_ = tx.Respond(res)
}

func (d *Dispatcher) handleCancel(req *sip.Request, tx sip.ServerTransaction) {
	// CANCEL 总是回 200；原 INVITE 事务由 sipgo 处理
	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	_ = tx.Respond(res)
}

func (d *Dispatcher) handleOptions(req *sip.Request, tx sip.ServerTransaction) {
	// OPTIONS 保活探测：回 200（使用 sipgo 对象，非手拼，红线#1）
	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	res.AppendHeader(sip.NewHeader("Accept", "application/sdp"))
	_ = tx.Respond(res)
}
