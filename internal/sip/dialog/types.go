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
	ID           ID
	RemoteURI    sip.Uri
	LocalURI     sip.Uri
	RemoteTarget sip.Uri  // 从 200 OK Contact 学习的目标地址
	RouteSet     []string // Record-Route（可选）
	cseq         atomic.Uint32
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
