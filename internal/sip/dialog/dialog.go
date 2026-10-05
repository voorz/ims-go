// Package dialog 提供 SIP 对话管理（WS-10）。
//
// 覆盖：dialog registry、CSeq 原子分配、dialog 内请求构建、PRACK。
package dialog

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/emiago/sipgo/sip"
)

// ID 是对话标识（Call-ID + 本地 tag + 远端 tag）。
type ID struct {
	CallID    string
	LocalTag  string
	RemoteTag string
}

func (id ID) String() string {
	return fmt.Sprintf("%s|%s|%s", id.CallID, id.LocalTag, id.RemoteTag)
}

// Dialog 是一条 SIP 对话。
type Dialog struct {
	ID        ID
	RemoteURI sip.Uri
	LocalURI  sip.Uri
	cseq      atomic.Uint32
}

// CSeq 分配下一个 CSeq（原子）。
func (d *Dialog) NextCSeq() uint32 {
	return d.cseq.Add(1)
}

// Registry 管理对话表。
type Registry struct {
	mu      sync.RWMutex
	dialogs map[string]*Dialog
	log     *slog.Logger
}

// NewRegistry 创建对话表。
func NewRegistry(log *slog.Logger) *Registry {
	if log == nil {
		log = slog.Default()
	}
	return &Registry{
		dialogs: make(map[string]*Dialog),
		log:     log,
	}
}

// Add 添加对话。
func (r *Registry) Add(d *Dialog) {
	r.mu.Lock()
	r.dialogs[d.ID.String()] = d
	r.mu.Unlock()
}

// Get 按 ID 查找对话。
func (r *Registry) Get(id ID) (*Dialog, bool) {
	r.mu.RLock()
	d, ok := r.dialogs[id.String()]
	r.mu.RUnlock()
	return d, ok
}

// Remove 删除对话。
func (r *Registry) Remove(id ID) {
	r.mu.Lock()
	delete(r.dialogs, id.String())
	r.mu.Unlock()
}

// Count 返回对话数。
func (r *Registry) Count() int {
	r.mu.RLock()
	n := len(r.dialogs)
	r.mu.RUnlock()
	return n
}

// NewInDialogRequest 构造对话内请求（INVITE/BYE/PRACK 等）。
func (d *Dialog) NewInDialogRequest(method sip.RequestMethod) *sip.Request {
	req := sip.NewRequest(method, d.RemoteURI)
	// Call-ID
	req.AppendHeader(sip.NewHeader("Call-ID", d.ID.CallID))
	// From（本地 tag）
	from := &sip.FromHeader{
		Address: d.LocalURI,
		Params:  sip.HeaderParams{{K: "tag", V: d.ID.LocalTag}},
	}
	req.AppendHeader(from)
	// To（远端 tag）
	to := &sip.ToHeader{
		Address: d.RemoteURI,
		Params:  sip.HeaderParams{{K: "tag", V: d.ID.RemoteTag}},
	}
	req.AppendHeader(to)
	// CSeq
	cseq := d.NextCSeq()
	req.AppendHeader(sip.NewHeader("CSeq", fmt.Sprintf("%d %s", cseq, method)))
	req.AppendHeader(sip.NewHeader("Content-Length", "0"))
	return req
}

// NewPRACK 构造 PRACK 请求（RFC 3262，可靠临时响应确认）。
func (d *Dialog) NewPRACK(rseq int) *sip.Request {
	req := d.NewInDialogRequest(sip.PRACK)
	// RAck: 响应序号 + CSeq
	rack := fmt.Sprintf("%d %d %s", rseq, d.cseq.Load(), sip.INVITE)
	req.AppendHeader(sip.NewHeader("RAck", rack))
	return req
}
