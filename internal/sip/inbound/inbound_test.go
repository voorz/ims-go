package inbound

import (
	"testing"

	"github.com/emiago/sipgo/sip"
)

// fakeVoiceHandler 模拟语音处理器。
type fakeVoiceHandler struct {
	inviteHandled bool
	byeHandled    bool
}

func (f *fakeVoiceHandler) HandleInvite(req *sip.Request, tx sip.ServerTransaction) bool {
	f.inviteHandled = true
	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	_ = tx.Respond(res)
	return true
}

func (f *fakeVoiceHandler) HandleBye(req *sip.Request, tx sip.ServerTransaction) bool {
	f.byeHandled = true
	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	_ = tx.Respond(res)
	return true
}

func TestDispatcherVoiceHandler(t *testing.T) {
	fh := &fakeVoiceHandler{}
	d := New(nil, fh)
	if d.voiceHandler == nil {
		t.Error("voiceHandler 为 nil")
	}
	// 验证接口满足
	var _ VoiceRequestHandler = fh
}

func TestDispatcherNoHandler(t *testing.T) {
	d := New(nil, nil)
	if d.voiceHandler != nil {
		t.Error("无处理器时期望 nil")
	}
}

func TestDialogProfile(t *testing.T) {
	p := DialogProfile{
		CallID:    "call-1",
		LocalTag:  "ltag",
		RemoteTag: "rtag",
		RemoteURI: "sip:bob@example.com",
	}
	if p.CallID != "call-1" {
		t.Error("DialogProfile 字段错误")
	}
}
