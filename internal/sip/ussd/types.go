// Package ussd 实现 USSD over IMS（WS-9）。
//
// 覆盖：SendUSSD/ContinueUSSD/CancelUSSD + XML 对象化编解码 +
// USSD 会话状态机。传输使用 SIP MESSAGE（会话状态在 USSD 层管理）。
package ussd

import (
	"log/slog"
	"sync"
	"time"

	"github.com/emiago/sipgo"
)

// ContentType 是 USSD XML 的媒体类型。
const ContentType = "application/vnd.3gpp.ussd+xml"

// State 是 USSD 会话状态。
type State int

const (
	StateIdle State = iota
	StateActive
	StateClosed
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateActive:
		return "active"
	case StateClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// Config 是 USSD 配置。
type Config struct {
	// IMPU 是本地标识。
	IMPU string
	// USSDTarget 是 USSD 网关地址（如 P-CSCF 或特定 AS）。
	USSDTarget string
	// PCSCFAddr 是 P-CSCF 地址。
	PCSCFAddr string
	// Client 是 sipgo 客户端。
	Client *sipgo.Client
	// SessionTimeout 是会话超时；0 用默认 5 分钟。
	SessionTimeout time.Duration
	// Logger 为空时用 slog 默认。
	Logger *slog.Logger
}

// Session 是一次 USSD 会话。
type Session struct {
	cfg Config
	log *slog.Logger

	mu        sync.RWMutex
	state     State
	sessionID string
	lastAt    time.Time
	timer     *time.Timer
}
