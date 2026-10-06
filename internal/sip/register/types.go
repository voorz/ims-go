// Package register 实现 SIP REGISTER 全流程（WS-5）。
//
// 覆盖：初始 REGISTER → 401/407 Digest-AKA（WS-2）→ 200 →
// sec-agree 494 处理 → 注册态机 → 刷新/注销定时器。
// 全部使用 sipgo 对象构造消息（红线#1，零 raw string）。
package register

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/emiago/sipgo"

	"github.com/voorz/ims-go/internal/sim"
)

// State 是注册状态机状态。
type State int

const (
	StateUnregistered State = iota
	StateRegistering
	StateRegistered
	StateUnregistering
	StateFailed
)

func (s State) String() string {
	switch s {
	case StateUnregistered:
		return "unregistered"
	case StateRegistering:
		return "registering"
	case StateRegistered:
		return "registered"
	case StateUnregistering:
		return "unregistering"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Config 是注册器配置。
type Config struct {
	// IMPU 是公有用户标识，如 "sip:alice@example.com"。
	IMPU string
	// IMPI 是私有用户标识，如 "alice@example.com"。
	IMPI string
	// HomeDomain 是归属域。
	HomeDomain string
	// PCSCFAddrs 是 P-CSCF 候选地址（host:port），按顺序尝试；
	// 503/连接失败时自动切换到下一个（带 penalty）。
	PCSCFAddrs []string
	// Contact 是本地 Contact URI。
	Contact string
	// Expires 是期望的注册有效期（秒）；0 用默认 600。
	Expires int
	// AKAProvider 用于 Digest-AKA（WS-2）。
	AKAProvider sim.AKAProvider
	// EAPRES 是 SWu 阶段 EAP-AKA 的 RES（16 进制字符串）。
	// 非空时启用 EAP 直接认证变体（复用 RES，避免 USIM SQN 双消耗）。
	EAPRES string
	// EnableVariantFallback 启用变体矩阵试错（默认 true）。
	// 关闭时只用 base 变体。
	EnableVariantFallback bool
	// HeaderOrder 指定 SIP 头序列化顺序（可选）。
	// 背景：RFC 3261 规定头顺序不应影响语义，但某些 P-CSCF 实现有 bug，
	// 对特定头顺序敏感（vowifi-core 在 Vodafone UK 遇到过）。
	// ims-go 不硬编码运营商分支（那是生产补丁写法）；而是提供通用机制：
	// 调用方经运营商配置传入顺序表，库按表重排，未在表中的头保持原相对顺序追加。
	// 为空表示不重排（默认）。
	HeaderOrder []string
	// Client 是 sipgo 客户端（传输由 WS-6 Pipeline 注入）。
	Client *sipgo.Client
	// OnStateChange 状态变更回调。
	OnStateChange func(from, to State)
	// OnDecision 决策记录回调（P2：P-CSCF 选择）。
	OnDecision func(d Decision)
	// Logger 为空时用 slog 默认。
	Logger *slog.Logger
}

// Decision 是一条 P-CSCF 选择决策记录（P2）。
type Decision struct {
	At       time.Time
	Selected string   // 选中的 P-CSCF
	Tried    []string // 已尝试的（含失败）
	Reason   string   // 选择原因
}

// Registration 是成功注册的信息。
type Registration struct {
	Expires   time.Time
	GRUU      string // 公有 GRUU（pub-gruu）
	TempGRUU  string // 临时 GRUU
	Contact   string
	ExpiresIn int // 秒
}

// Registrar 管理 REGISTER 生命周期。
type Registrar struct {
	cfg Config
	log *slog.Logger

	mu       sync.RWMutex
	state    State
	reg      *Registration
	cancel   context.CancelFunc
	nonceCnt int
	cnonce   string

	// penalty 记录 P-CSCF 失败次数，用于选择排序
	penalty map[string]int

	// learnedVariant 是已学习的成功变体名（P1）。
	// 下次 REGISTER 优先尝试，实现 O(1) 命中。
	learnedVariant string
	// reachedAuth 标记是否已到达认证阶段（收到 401/407）。
	// 为 true 后不再切换 P-CSCF（vowifi-core 生产经验）。
	reachedAuth bool
	// lastCallID/lastCSeq/lastAuth 供 protected refresh 复用。
	lastCallID string
	lastCSeq   int
	lastAuth   string
}
