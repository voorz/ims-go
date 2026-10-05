package ims

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// ==================== Config ====================

// Config 是 ims-go 唯一的对外配置入口（D-007：单套分组 Config，一次校验，零转换）。
// Modules 承载各子系统的 interface 注入（D-010）：为 nil 的槽位表示该能力未装配。
type Config struct {
	SIM       SIMConfig
	SWu       SWuConfig
	SIP       SIPConfig
	Voice     VoiceConfig
	Carrier   CarrierConfig
	Dataplane DataplaneConfig
	Logging   LoggingConfig
	Recovery  RecoveryPolicy
	Modules   Modules
}

// SIMConfig：SIM/AKA 相关配置。AKAProvider（硬件 SIM）由消费方注入，WS-2 细化。
type SIMConfig struct {
	SoftSIM SoftSIMConfig
}

// SoftSIMConfig：软 SIM（milenage）开关（D-015）。
type SoftSIMConfig struct {
	// Enable 默认 false；启用后仅允许 3GPP 测试钥进入软件侧，生产钥由 lint 门禁拦截。
	Enable bool
}

// SWuConfig：SWu/IKEv2 隧道配置。WS-3 细化（ePDG 地址、重传定时器等）。
type SWuConfig struct{}

// SIPConfig：SIP 协议栈配置。WS-5/WS-6/WS-7 细化（注册参数、传输参数等）。
type SIPConfig struct{}

// VoiceConfig：语音配置。WS-11/WS-12 细化（编解码偏好、DTMF 模式等）。
type VoiceConfig struct{}

// CarrierConfig：运营商配置。WS-13 细化（档案覆盖、推导开关等）。
type CarrierConfig struct{}

// DataplaneMode：数据面模式（D-013）。
type DataplaneMode string

const (
	// DataplaneUserspace 默认模式：进程内 ESP 加解密泵，免特权。
	DataplaneUserspace DataplaneMode = "userspace"
	// DataplaneTUN 显式 opt-in：真实 TUN 网卡，需 CAP_NET_ADMIN。
	DataplaneTUN DataplaneMode = "tun"
	// DataplaneXFRMI 显式 opt-in：内核 XFRM，需特权与内核支持。
	DataplaneXFRMI DataplaneMode = "xfrmi"
)

// DataplaneConfig：数据面配置。
type DataplaneConfig struct {
	// Mode 为空时默认为 DataplaneUserspace（D-013）。
	Mode DataplaneMode
}

// LoggingConfig：日志配置。直接使用标准库 slog（D-008），不建日志包装层。
type LoggingConfig struct {
	Level  slog.Level
	Source bool // 是否记录调用位置
}

// RecoveryPolicy：模块恢复策略（H3）。模块非预期退出时，监督器按此策略重启。
type RecoveryPolicy struct {
	// Disabled 为 true 时模块异常退出后不再重启。
	Disabled bool
	// MaxRestarts 最大重启次数；0 表示默认 5；<0 非法。
	MaxRestarts int
	// InitialBackoff 首次重启退避；0 表示默认 1s；<0 非法。
	InitialBackoff time.Duration
	// MaxBackoff 退避上限；0 表示默认 30s；<0 非法。
	MaxBackoff time.Duration
}

// ConfigError 是结构化的配置校验错误。
type ConfigError struct {
	Field  string
	Reason string
}

func (e *ConfigError) Error() string {
	return "ims: 非法配置 [" + e.Field + "]：" + e.Reason
}

// ==================== Modules ====================

// Module 是所有子模块的公共生命周期契约。
// Start 应阻塞直到模块停止或失败；返回非 nil 错误且客户端仍在运行时，
// 监督器视为非预期退出并按 RecoveryPolicy 重启。Stop 应使阻塞中的 Start 返回。
type Module interface {
	Start(ctx context.Context) error
	Stop() error
}

// Modules：子系统 interface 注入点（D-010，P3 可替换性）。
// 为 nil 的槽位表示该能力未装配，调用对应 Client 方法时返回哨兵错误。
type Modules struct {
	Tunnel Module      // SWu/IKEv2 隧道。WS-3 细化接口。
	SIP    Module      // SIP 协议栈。WS-5/WS-6/WS-7 细化接口。
	SMS    SMSModule   // 短信。WS-8 实现。
	USSD   USSDModule  // USSD。WS-9 实现。
	Voice  VoiceModule // 语音。WS-11 实现。
}

// SMSModule：短信能力。
type SMSModule interface {
	Module
	Send(ctx context.Context, req SMSRequest) (*SMSResult, error)
}

// USSDModule：USSD 能力。
type USSDModule interface {
	Module
	Send(ctx context.Context, code string) (*USSDResult, error)
}

// VoiceModule：语音能力。
type VoiceModule interface {
	Module
	Dial(ctx context.Context, req CallRequest) (*Call, error)
	Hangup(ctx context.Context, callID string) error
}

// ==================== Client ====================

// Client 是 ims-go 的唯一入口（D-010）：一次构造、一直使用。
// 内部收回生命周期管理、期望态对账、恢复策略（H2/H3）。
type Client struct {
	cfg       Config
	state     atomic.Int32
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	disp      *dispatcher
	startedAt time.Time
	modState  map[string]bool
	decisions []DecisionRecord
}

// ==================== Status / Event / Decision ====================

// ClientState：客户端生命周期状态（强类型，H7）。
type ClientState string

const (
	StateStopped  ClientState = "stopped"
	StateStarting ClientState = "starting"
	StateRunning  ClientState = "running"
	StateStopping ClientState = "stopping"
)

// ModuleStatus：单个模块的运行状态。
type ModuleStatus struct {
	Name    string
	Running bool
}

// Status：客户端状态快照（强类型，H7）。
type Status struct {
	State     ClientState
	StartedAt time.Time
	Modules   []ModuleStatus
}

// EventType：事件类型。WS-5/WS-8/WS-11 等追加注册/短信/呼叫相关类型。
type EventType string

const (
	EventClientStarted   EventType = "client.started"
	EventClientStopped   EventType = "client.stopped"
	EventModuleStarted   EventType = "module.started"
	EventModuleFailed    EventType = "module.failed"
	EventModuleRestarted EventType = "module.restarted"
	EventModuleStopped   EventType = "module.stopped"
)

// Event：单一事件通道投递的事件（H5：消灭多通道）。
type Event struct {
	Type   EventType
	At     time.Time
	Module string // 相关模块名，可空
	Reason string // 人类可读的原因
}

// EventHandler：事件处理函数。处理函数不得阻塞过久；
// 分发器为每个订阅者提供独立缓冲，慢消费者会被隔离并计数丢弃。
type EventHandler func(Event)

// DecisionRecord：结构化决策记录（P2）。
// 每个"同一件事只一处做决定"的决策点都 emit 一条，不再靠日志考古。
type DecisionRecord struct {
	At     time.Time
	Point  string   // 决策点，如 "module-recovery"
	Inputs []string // 关键输入摘要
	Chosen string   // 选中的分支
	Reason string   // 原因
}

// ==================== SMS / USSD / Voice ====================

// SMSRequest：短信发送请求。
type SMSRequest struct {
	To   string
	Text string
}

// SMSResult：短信发送结果。
type SMSResult struct {
	MessageID string
	Segments  int
}

// USSDResult：USSD 会话结果。
type USSDResult struct {
	SessionID string
	Text      string
	HasMore   bool // true 表示对端期待继续输入
}

// CallRequest：呼叫请求。
type CallRequest struct {
	To string
}

// Call：一次语音呼叫的句柄。
type Call struct {
	ID    string
	voice VoiceModule
}

// VoiceControl：语音网关访问器（Client.Voice 返回）。
type VoiceControl struct {
	m VoiceModule
}

// ==================== Errors ====================

var (
	ErrAlreadyRunning = errors.New("ims: 客户端已在运行")
	ErrNotRunning     = errors.New("ims: 客户端未运行")
	ErrNoSMSModule    = errors.New("ims: SMS 模块未配置")
	ErrNoUSSDModule   = errors.New("ims: USSD 模块未配置")
	ErrNoVoiceModule  = errors.New("ims: 语音模块未配置")
)
