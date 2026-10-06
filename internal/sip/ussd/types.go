// Package ussd 实现 USSD over IMS（3GPP TS 24.390）。
//
// 模型：INVITE（multipart SDP+USSD XML）建 dialog → INFO（g.3gpp.ussd）交互 → BYE 结束。
// 与 vowifi-core/vowifi-go 一致；之前误用的 SIP MESSAGE 模型已废除。
package ussd

import (
	"log/slog"
	"sync"
	"time"

	"github.com/emiago/sipgo"
)

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

// Result 是 USSD 操作结果。
type Result struct {
	Text      string // 解码后的 USSD 文本
	Status    int    // 0=完成，1=需继续（菜单），2=失败，5=超时
	SessionID string // 会话 ID（Status=1 时有效）
	RawXML    string // 原始 XML（调试用）
}

// InfoResult 是入站 INFO/BYE 投递给等待操作的结果。
type InfoResult struct {
	Text   string
	RawXML string
	Err    error
}

// Config 是 USSD 服务配置。
type Config struct {
	// IMPU 是本地公有标识（如 sip:user@ims.mnc...）。
	IMPU string
	// Domain 是归属域（如 ims.mnc033.mcc234.3gppnetwork.org）。
	Domain string
	// PCSCFAddr 是 P-CSCF 地址。
	PCSCFAddr string
	// Contact 是本地 Contact。
	Contact string
	// Client 是 sipgo 客户端。
	Client *sipgo.Client
	// Server 是 sipgo 服务端（接收入站 INFO/BYE）。
	Server *sipgo.Server
	// SessionTimeout 是会话无活动超时；0 用默认 5 分钟。
	SessionTimeout time.Duration
	// Logger 为空时用 slog 默认。
	Logger *slog.Logger
}

// Session 是单个 USSD dialog 会话。
type Session struct {
	mu           sync.Mutex
	id           string
	callID       string
	localTag     string
	remoteTag    string
	remoteTarget string
	cseq         uint32
	state        State
	resultCh     chan InfoResult
	createdAt    time.Time
	lastAt       time.Time
	timer        *time.Timer
}

// ID 返回会话 ID。
func (s *Session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// IsActive 报告会话是否可接受操作。
func (s *Session) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == StateActive
}

// Service 拥有至多一个活动 USSD 会话（USSD 本质串行）。
type Service struct {
	cfg Config
	log *slog.Logger

	mu      sync.Mutex
	session *Session
}

// USSD 会话级常量。
const (
	// ContentType 是 USSD XML 的媒体类型。
	ContentType = "application/vnd.3gpp.ussd+xml"
	// InfoPackage 是 INFO 的包名。
	InfoPackage = "g.3gpp.ussd"
	// multipartBoundary 是 INVITE multipart 的 boundary。
	multipartBoundary = "imsgo_ussd"
	// transactionTimeout 是单次 SIP 事务超时。
	transactionTimeout = 45 * time.Second
)
