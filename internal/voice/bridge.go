package voice

import (
	"github.com/emiago/sipgo/sip"
)

// Bridge 实现 inbound.VoiceRequestHandler（H1：入站桥接一等能力）。
//
// 入站 INVITE → 创建 incoming Call → 回调 OnIncomingCall →
// 消费方决定接听/拒绝。消灭消费方手写 B2BUA。
type Bridge struct {
	agent *Agent
}

// NewBridge 创建桥接器。
func NewBridge(agent *Agent) *Bridge {
	return &Bridge{agent: agent}
}

// HandleInvite 处理入站 INVITE。
func (b *Bridge) HandleInvite(req *sip.Request, tx sip.ServerTransaction) bool {
	callID := ""
	if h := req.GetHeader("Call-ID"); h != nil {
		callID = h.Value()
	}
	remoteURI := req.Recipient.Host
	call := &Call{
		ID:        "in-" + callID,
		State:     StateInit,
		Direction: "incoming",
		RemoteURI: remoteURI,
	}
	ca := b.agent.newCallActor(call)
	ca.do(func() {
		ca.call.State = StateRinging
	})
	// 先回 180 Ringing
	res := sip.NewResponseFromRequest(req, 180, "Ringing", nil)
	_ = tx.Respond(res)

	if b.agent.cfg.OnIncomingCall != nil {
		b.agent.cfg.OnIncomingCall(call)
	}
	return true
}

// HandleBye 处理入站 BYE。
func (b *Bridge) HandleBye(req *sip.Request, tx sip.ServerTransaction) bool {
	callID := ""
	if h := req.GetHeader("Call-ID"); h != nil {
		callID = "in-" + h.Value()
	}
	_ = b.agent.transition(callID, StateTerminating)
	_ = b.agent.transition(callID, StateTerminated)
	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	_ = tx.Respond(res)
	return true
}

// Answer 接听入站呼叫（200 OK）。
// 注意：需要原始 tx；简化实现中由调用方持有 tx。
func (b *Bridge) Answer(callID string, tx sip.ServerTransaction, req *sip.Request) error {
	if err := b.agent.transition(callID, StateConnected); err != nil {
		return err
	}
	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	res.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
	sdp := "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 5004 RTP/AVP 0\r\n"
	res.SetBody([]byte(sdp))
	return tx.Respond(res)
}

// Reject 拒绝入站呼叫。
func (b *Bridge) Reject(callID string, tx sip.ServerTransaction, req *sip.Request, code int, reason string) error {
	_ = b.agent.transition(callID, StateTerminating)
	_ = b.agent.transition(callID, StateTerminated)
	res := sip.NewResponseFromRequest(req, code, reason, nil)
	return tx.Respond(res)
}
