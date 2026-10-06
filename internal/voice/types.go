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

	// Dialog 状态（INVITE 建立后填充）
	CallID       string
	LocalTag     string
	RemoteTag    string
	RemoteTarget string
	CSeq         uint32

	// 补充业务状态
	LocalHold  bool // 本地 hold
	RemoteHold bool // 远端 hold

	// Session Timer（RFC 4028）
	SessionExpires   int    // 秒；0 表示未协商
	SessionRefresher string // "uac" / "uas"
	SessionTimer     *time.Timer

	// 媒体
	SDP       string // 本地 SDP
	RemoteSDP string // 远端 SDP
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
}

// Agent 是 per-device 语音实例（D-014）。
type Agent struct {
	cfg Config
	log *slog.Logger

	mu    sync.RWMutex
	calls map[string]*callActor
}

// callActor 是单呼叫的 Actor（单 goroutine 串行状态转移）。
type callActor struct {
	call *Call
	ch   chan func()
	done chan struct{}
}
