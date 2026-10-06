// Package voice 实现语音呼叫引擎（WS-11，D-014）。
//
// 模型：per-device Agent + 显式 8 状态机 + Actor 单 goroutine 串行 +
// 双腿桥接。状态机为数据驱动 transitionMap；全部 sipgo 对象（红线#1）。
package voice

import (
	"log/slog"
	"sync"
	"time"

	"github.com/emiago/sipgo"

	"github.com/voorz/ims-go/internal/sip/dialog"
)

// State 是呼叫状态（8 状态机）。
type State int

const (
	StateInit State = iota
	StateCalling
	StateRinging
	StateEarlyMedia
	StatePreconditionWait
	StateConnected
	StateTerminating
	StateTerminated
)

func (s State) String() string {
	switch s {
	case StateInit:
		return "Init"
	case StateCalling:
		return "Calling"
	case StateRinging:
		return "Ringing"
	case StateEarlyMedia:
		return "EarlyMedia"
	case StatePreconditionWait:
		return "PreconditionWait"
	case StateConnected:
		return "Connected"
	case StateTerminating:
		return "Terminating"
	case StateTerminated:
		return "Terminated"
	default:
		return "Unknown"
	}
}

// 别名（兼容常用命名）
const (
	StateIdle         = StateInit
	StateDialing      = StateCalling
	StateAlerting     = StateRinging
	StateConnecting   = StateEarlyMedia
	StateDisconnected = StateTerminating
	StateEnded        = StateTerminated
)

// transitionMap 是数据驱动的状态转移表（D-014）。
var transitionMap = map[State][]State{
	StateInit:             {StateCalling, StateRinging}, // 主叫/被叫
	StateCalling:          {StateRinging, StateEarlyMedia, StateConnected, StateTerminating},
	StateRinging:          {StateEarlyMedia, StateConnected, StateTerminating},
	StateEarlyMedia:       {StateConnected, StateTerminating},
	StatePreconditionWait: {StateConnected, StateTerminating},
	StateConnected:        {StateTerminating},
	StateTerminating:      {StateTerminated},
	StateTerminated:       {},
}

// CanTransition 报告是否允许从 from 到 to。
func CanTransition(from, to State) bool {
	for _, s := range transitionMap[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Call 是一路呼叫。
type Call struct {
	ID        string
	State     State
	Direction string // "outgoing" / "incoming"
	RemoteURI string
	At        time.Time

	// Dialog 句柄（INVITE 2xx 后经 LearnFromResponse 建立，不可绕过）。
	// 所有 dialog 内请求必须经 Dialog 发，禁止手工拼装（D-007）。
	Dialog *dialog.Dialog

	// 补充业务状态
	LocalHold  bool // 本地 hold
	RemoteHold bool // 远端 hold

	// Session Timer（RFC 4028）：由 sessionTimer 状态机管理，见 session_timer.go
	SessionExpires   int           // 秒；0 表示未协商
	SessionRefresher string        // "uac" / "uas"
	sessionTimer     *sessionTimer // 内部 timer（小写，外部经方法访问）

	// 媒体
	SDP       string // 本地 SDP
	RemoteSDP string // 远端 SDP

	// 幂等释放：防止 CANCEL/BYE/超时三路并发重复释放
	finalizeOnce sync.Once
	// No-answer timer
	noAnswerTimer *time.Timer
}

// Config 是 Agent 配置。
type Config struct {
	// IMPU 是本地标识。
	IMPU string
	// PCSCFAddr 是 P-CSCF 地址。
	PCSCFAddr string
	// Client 是 sipgo 客户端。
	Client *sipgo.Client
	// Server 是 sipgo 服务端（入站）。
	Server *sipgo.Server
	// OnStateChange 状态变更回调。
	OnStateChange func(callID string, from, to State)
	// OnIncomingCall 入站呼叫回调（H1 桥接）。
	OnIncomingCall func(call *Call)
	// Logger 为空时用 slog 默认。
	Logger *slog.Logger

	// 以下为用户偏好（从 ims.VoiceConfig 映射）：
	// Codecs 是 SDP offer 编码偏好；空用默认。
	Codecs []string
	// DTMFMode 是 DTMF 模式："rfc4733"（默认）或 "inband"。
	DTMFMode string
	// MaxCalls 是最大并发；0 用默认 2。
	MaxCalls int
	// DisableSessionTimer 为 true 时关闭 Session Timer。
	DisableSessionTimer bool
	// NoAnswerTimeout 是未接听超时；0 用默认 60s。
	NoAnswerTimeout time.Duration
}

// Agent 是 per-device 语音实例（D-014）。
type Agent struct {
	cfg Config
	log *slog.Logger

	mu            sync.RWMutex
	calls         map[string]*callActor
	referSessions map[string]*referSession // callID → REFER 会话
}

// callActor 是单呼叫的 Actor（单 goroutine 串行状态转移）。
type callActor struct {
	call *Call
	ch   chan func()
	done chan struct{}
}

// Bridge 实现 inbound.VoiceRequestHandler（H1：入站桥接一等能力）。
//
// 入站 INVITE → 创建 incoming Call → 回调 OnIncomingCall →
// 消费方决定接听/拒绝。消灭消费方手写 B2BUA。
type Bridge struct {
	agent *Agent
}
