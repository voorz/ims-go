// Package subscribe 实现 SUBSCRIBE/NOTIFY（WS-7）。
//
// 覆盖：SUBSCRIBE(reg) 独立 Call-ID、Security-Verify 继承、
// 生命周期与重订阅定时；NOTIFY 分发（reginfo 解析、MWI、背压队列）；
// 注册丢失→订阅重建联动（由调用方触发 Resubscribe）。
package subscribe

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/emiago/sipgo"
)

// State 是订阅状态。
type State int

const (
	StateIdle State = iota
	StateSubscribing
	StateActive
	StateFailed
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateSubscribing:
		return "subscribing"
	case StateActive:
		return "active"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Config 是订阅器配置。
type Config struct {
	// IMPU 是订阅的公有标识。
	IMPU string
	// Event 是订阅事件，如 "reg"。
	Event string
	// Expires 是订阅有效期（秒）；0 用默认 3600。
	Expires int
	// SecurityVerify 从 REGISTER 200 OK 继承的 Security-Server 值。
	SecurityVerify string
	// PCSCFAddr 是 P-CSCF 地址。
	PCSCFAddr string
	// Contact 是本地 Contact。
	Contact string
	// Client 是 sipgo 客户端。
	Client *sipgo.Client
	// Server 是 sipgo 服务端（接收 NOTIFY）。
	Server *sipgo.Server
	// OnNotify 是 NOTIFY 回调（在独立 goroutine 中调用，注意并发）。
	OnNotify func(n NotifyEvent)
	// OnStateChange 状态变更回调。
	OnStateChange func(from, to State)
	// NotifyQueueSize 是 NOTIFY 背压队列长度；0 用默认 16。
	NotifyQueueSize int
	// Logger 为空时用 slog 默认。
	Logger *slog.Logger
}

// NotifyEvent 是一条解析后的 NOTIFY。
type NotifyEvent struct {
	At      time.Time
	Event   string
	RegInfo *RegInfo // Event: reg 时解析
	MWI     *MWI     // message-summary 时解析
	Raw     []byte   // 原始消息体（调试用）
}

// RegInfo 是 RFC 3680 reginfo 文档的解析结果。
type RegInfo struct {
	Version  string
	Contacts []RegContact
}

// RegContact 是一条注册联系人记录。
type RegContact struct {
	URI   string
	State string // active/terminated
	Event string // registered/created/refreshed/deactivated/unregistered
}

// MWI 是消息等待指示。
type MWI struct {
	Waiting bool
	Account string
}

// Subscriber 管理 SUBSCRIBE 生命周期。
type Subscriber struct {
	cfg Config
	log *slog.Logger

	mu       sync.RWMutex
	state    State
	callID   string
	localTag string
	cseq     int
	notifyQ  chan NotifyEvent
	cancel   context.CancelFunc
}
