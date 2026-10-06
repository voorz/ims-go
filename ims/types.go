package ims

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/voorz/ims-go/internal/identity"
	"github.com/voorz/ims-go/internal/sim"
)

// ==================== Config ====================

// Config 是 ims-go 唯一的对外配置入口（D-007：单套分组 Config，一次校验，零转换）。
// Modules 承载各子系统的 interface 注入（D-010）：为 nil 的槽位表示该能力未装配。
type Config struct {
	SIM       SIMConfig
	SWu       SWuConfig
	SIP       SIPConfig
	SMS       SMSConfig
	Voice     VoiceConfig
	Carrier   CarrierConfig
	Dataplane DataplaneConfig
	Logging   LoggingConfig
	Recovery  RecoveryPolicy
	Modules   Modules
}

// SMSConfig：短信配置（WS-8）。
type SMSConfig struct {
	// Store 是投递存储；nil 时用内存实现（进程重启丢失）。
	// 主项目可实现 SMSDeliveryStore 做持久化（如 SQLite）。
	Store SMSDeliveryStore
}

// SIMConfig：SIM/AKA 相关配置。
type SIMConfig struct {
	// AKAProvider 由消费方注入（硬件 SIM 经 APDU/QMI 调制解调器，D-010）。
	AKAProvider AKAProvider
	SoftSIM     SoftSIMConfig
}

// AKAProvider 是公开的 AKA 契约（消费方实现）。
// 内部适配为 sim.AKAProvider（红线#9：对外不暴露 internal 类型）。
type AKAProvider interface {
	CalculateAKA(rand16, autn16 []byte) (AKAResult, error)
}

// AKAResult 是 AKA 计算结果（公开类型）。
type AKAResult struct {
	RES  []byte // 认证响应
	CK   []byte // 加密密钥
	IK   []byte // 完整性密钥
	AUTS []byte // 重同步令牌（同步失败时）
}

// SoftSIMConfig：软 SIM（milenage）开关（D-015）。
type SoftSIMConfig struct {
	// Enable 默认 false；启用后仅允许测试密钥（ims.TestKeys / ims.CustomTestKeys）。
	Enable bool
	// Keys 测试密钥；Enable 时必须有效。
	Keys MilenageKeys
}

// MilenageKeys 是软 SIM 测试密钥的不透明句柄（D-015）。
// 只能由 TestKeys / CustomTestKeys 构造；内部持有 sim.MilenageKeys（不暴露）。
type MilenageKeys struct {
	inner sim.MilenageKeys
}

// Valid 报告密钥是否有效。
func (k MilenageKeys) Valid() bool { return k.inner.Valid() }

// TestKeys 返回 3GPP 测试密钥（仅测试/实验室用）。
func TestKeys() MilenageKeys { return MilenageKeys{inner: sim.TestKeys()} }

// CustomTestKeys 由给定的 K/OP 构造测试密钥。
func CustomTestKeys(k, op []byte, useOPc bool) (MilenageKeys, error) {
	inner, err := sim.CustomTestKeys(k, op, useOPc)
	if err != nil {
		return MilenageKeys{}, err
	}
	return MilenageKeys{inner: inner}, nil
}

// simAKAAdapter 将公开 AKAProvider 适配为内部 sim.AKAProvider。
type simAKAAdapter struct{ p AKAProvider }

func (a simAKAAdapter) CalculateAKA(rand16, autn16 []byte) (sim.AKAResult, error) {
	r, err := a.p.CalculateAKA(rand16, autn16)
	if err != nil {
		return sim.AKAResult{}, err
	}
	return sim.AKAResult{RES: r.RES, CK: r.CK, IK: r.IK, AUTS: r.AUTS}, nil
}

// toSimAKAProvider 将公开 AKAProvider 适配为内部 sim.AKAProvider。
func toSimAKAProvider(p AKAProvider) sim.AKAProvider {
	if p == nil {
		return nil
	}
	return simAKAAdapter{p: p}
}

// SWuConfig：SWu/IKEv2 隧道配置（WS-3 收敛后的公开子集）。
// ProxyConfig：SOCKS5 代理配置（A7，精简版）。
// 用于按 PLMN 的地理路由：默认直连，仅在需要时启用。
// 对标 vowifi-core ProxyConfig，但去重（Addr/Host/Address 三字段合并为一）。
type ProxyConfig struct {
	// Addr 是代理地址（host:port），如 "1.2.3.4:1080"。
	Addr string
	// Username/Password 可选（无认证时为空）。
	Username string
	Password string
	// Enabled 为 true 时启用代理；false 或 nil ProxyConfig 表示直连。
	Enabled bool
}

// SWuTransportFactory：SWu 传输工厂（A7，可插拔）。
// 主项目可注入自己的传输实现（SOCKS5 或其他代理协议）；
// nil 时库内按 ProxyConfig 创建默认 SOCKS5 传输，无 Proxy 时直连。
type SWuTransportFactory func(local, remote string) (SWuTransport, error)

// SWuTransport：SWu 层的数据报传输抽象。
type SWuTransport interface {
	WriteTo(p []byte, addr string) (int, error)
	ReadFrom(p []byte) (int, string, error)
	Close() error
}

// 内部完整配置见 internal/swu.Config；映射集中在 ims/swu.go（单处，D-007）。
type SWuConfig struct {
	// EPDGAddrs 是 ePDG 候选地址（域名或 IP）。
	// 为空时按 MCC/MNC 做 DNS 发现。
	EPDGAddrs []string
	IMSI      string
	MCC       string
	MNC       string
	APN       string
	// LocalAddr/LocalPort 是本地绑定；为空时自动选择。
	LocalAddr string
	LocalPort uint16
	// AlgorithmPolicy 是算法策略（strict/balanced/legacy_prefer），为空用 balanced。
	AlgorithmPolicy string
	// IKEProposals/ESPProposals 为空时用内部默认提议。
	IKEProposals []string
	ESPProposals []string
	// 定时器：0 表示使用内部默认值。
	RekeyIKE     time.Duration
	RekeyChild   time.Duration
	Reauth       time.Duration
	NATKeepalive time.Duration
	DPD          time.Duration
	// WiresharkKeyLogPath 是 ESP 密钥日志路径（排障用，D-013）。
	WiresharkKeyLogPath string
	// Proxy 是可选的 SOCKS5 代理（A7）；nil 或 !Enabled 时直连。
	Proxy *ProxyConfig
	// TransportFactory 是可选的自定义传输工厂（A7）；nil 时用默认实现。
	TransportFactory SWuTransportFactory
}

// SIPConfig：SIP 协议栈配置。WS-5/WS-6/WS-7 细化（注册参数、传输参数等）。
type SIPConfig struct {
	// IMPU 是公有用户标识，如 "sip:alice@example.com"。
	IMPU string
	// IMPI 是私有用户标识，如 "alice@example.com"。
	IMPI string
	// HomeDomain 是归属域。
	HomeDomain string
	// PCSCFAddrs 是 P-CSCF 候选地址（host:port）。
	PCSCFAddrs []string
	// Contact 是本地 Contact URI。
	Contact string
	// RegisterExpires 是注册有效期（秒）；0 用默认 600。
	RegisterExpires int
	// SubscribeExpires 是订阅有效期（秒）；0 用默认 3600。
	SubscribeExpires int
	// CellID 是蜂窝小区标识（A5），用于 PANI 头注入；为空时只发 IEEE-802.11。
	// 格式：utran-cell-id-3gpp 值，如 "46000123456789"。
	CellID string
	// EAPRES 是 SWu 阶段 EAP-AKA 的 RES（可选，启用 EAP 直接认证）。
	EAPRES string
	// Dialer 是传输预拨号器（经 IPsec 隧道）；nil 时用 net.Dialer 直连（仅测试）。
	// 生产环境应注入经隧道接口的 Dialer。
	Dialer Dialer
	// VariantStore 是 REGISTER 变体学习持久化；nil 时仅内存学习（进程重启丢失）。
	VariantStore VariantStore
}

// Dialer 通过底层网络（隧道内）拨号。
// 与 internal/sip/transport.Dialer 同构，公开以便用户注入。
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// VariantStore 是 REGISTER 变体学习存储接口。
// 与 internal/sip/register.VariantStore 同构，公开以便用户注入持久化。
// key 为 IMPU；value 为成功变体名。
type VariantStore interface {
	// LoadVariant 加载已学习的变体；不存在返回 ("", nil)。
	LoadVariant(impu string) (string, error)
	// SaveVariant 保存成功的变体。
	SaveVariant(impu, variant string) error
}

// VoiceConfig：语音用户偏好（WS-11）。
// 字段均为用户可调的业务偏好；内部 wiring（Client/Server/IMPU 等）由库装配，不暴露。
type VoiceConfig struct {
	// Codecs 是 SDP offer 中的编码偏好顺序，如 ["AMR-WB", "AMR"]。
	// 空用默认 ["AMR-WB", "AMR", "telephone-event"]。
	// 实际场景：某些运营商只接受 AMR-NB，需去掉 AMR-WB。
	Codecs []string
	// DTMFMode 是 DTMF 发送模式："rfc4733"（默认）或 "inband"。
	// 实际场景：某些网络的 DTMF 网关对 RFC 4733 支持不佳。
	DTMFMode string
	// MaxCalls 是最大并发呼叫数；0 用默认 2。
	// 实际场景：单卡设备通常 1-2 路。
	MaxCalls int
	// DisableSessionTimer 为 true 时关闭 Session Timer（RFC 4028）。
	// 实际场景：某些 P-CSCF 对 Session-Expires 处理有 bug。
	DisableSessionTimer bool
	// NoAnswerTimeout 是未接听超时；0 用默认 60s。
	NoAnswerTimeout time.Duration
	// OnIncomingCall 是入站呼叫回调（H1）。
	// 为 nil 时，若装配了语音模块则由内部处理；主项目可实现 IncomingCallHandler 接管。
	OnIncomingCall IncomingCallHandler
	// Audio 是 PCM 音频接口；nil 时语音无音频（仅信令）。
	// VoWiFi 语音的音频由 ims 库负责 RTP 打包，PCM 由消费方提供（麦克风/扬声器）。
	Audio AudioIO
}

// AudioIO 是 PCM 音频接口（VoWiFi 语音）。
// 采样率 8000Hz（AMR）或 16000Hz（AMR-WB），16-bit 单声道 PCM。
// 实现方负责音频设备的打开/关闭；库负责 RTP 打包/解包。
type AudioIO interface {
	// ReadPCM 读取一帧 PCM（160 或 320 采样，20ms）。
	// 返回 (pcm, false, nil) 表示静音帧；(nil, true, nil) 表示流结束。
	ReadPCM() (pcm []int16, end bool, err error)
	// WritePCM 写入一帧解码后的 PCM。
	WritePCM(pcm []int16) error
	// SampleRate 返回采样率（8000 或 16000）。
	SampleRate() int
	// Close 关闭音频设备。
	Close() error
}

// IncomingCallHandler 处理入站语音呼叫（H1 桥接的公开契约）。
// 相比内部 inbound.VoiceRequestHandler，本接口只暴露主项目需要的语义，
// 不涉及 sipgo 事务对象。
// IncomingCallRequest：入站 INVITE 的完整上下文（A3）。
type IncomingCallRequest struct {
	From      string            // 主叫标识
	CallID    string            // 呼叫 ID
	RemoteSDP string            // 远端 SDP offer，可空
	Headers   map[string]string // 关键 SIP 头（P-Asserted-Identity 等），可空
}

// IncomingCallResponse：handler 对入站呼叫的裁决（A3）。
type IncomingCallResponse struct {
	Accept     bool   // true=接管（库不再做默认处理）
	StatusCode int    // 拒绝时的 SIP 状态码（486/603…），Accept=false 时有效
	Reason     string // 拒绝原因，人类可读
	LocalSDP   string // 接受时的 SDP answer，Accept=true 时可空（库生成默认）
}

type IncomingCallHandler interface {
	// HandleIncomingCall 收到入站 INVITE 时调用。
	// 返回 Accept=true 表示接管；Accept=false + StatusCode 表示拒绝。
	HandleIncomingCall(ctx context.Context, req IncomingCallRequest) IncomingCallResponse
}

// CarrierConfig：运营商覆盖（WS-13 内部模型的公开子集）。
// 这是"JSON override"层：用户显式指定的值优先于 preset/推导/学习。
// 优先级：本结构字段 > LearnedProfile > preset > 推导。
type CarrierConfig struct {
	// MCCMNC 如 "23415"；空则自动推导（SIM/网络）。
	// 实际场景：测试、MVNO、手动指定。
	MCCMNC string
	// EPDGAddr 覆盖 ePDG 地址；空则用 preset/推导。
	// 实际场景：自定义 APN、企业专线。
	EPDGAddr string
}

// CarrierProfileYAML：云端 YAML profile 的公开表示（A6）。
// 主项目用于展示和手动保存。
type CarrierProfileYAML struct {
	Version        int
	Kind           string
	ID             string
	SupportedPLMNs []string
}

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
	// Continue 回复当前 USSD 会话的菜单/输入（交互式 USSD 必需）。
	Continue(ctx context.Context, input string) (*USSDResult, error)
	// Cancel 取消当前 USSD 会话。
	Cancel(ctx context.Context) error
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
	identity  *identity.Identity // A4：PrepareStart 产出的身份，可空
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
	// 业务事件（A1）：携带 Data 负载。
	EventSMSReceived        EventType = "sms.received"
	EventSMSSent            EventType = "sms.sent"
	EventLocalNumberLearned EventType = "identity.local_number_learned"
	EventRegistrationFailed EventType = "register.failed"
)

// SMSReceivedData：EventSMSReceived 的 Data 负载。
type SMSReceivedData struct {
	From    string
	Content string
	At      time.Time
}

// SMSSentData：EventSMSSent 的 Data 负载。
type SMSSentData struct {
	To      string
	MsgID   string
	Success bool
}

// LocalNumberLearnedData：EventLocalNumberLearned 的 Data 负载。
type LocalNumberLearnedData struct {
	Number string
	Source string // 学习来源：p-associated-uri / from-header / ...
}

// RegistrationFailedData：EventRegistrationFailed 的 Data 负载。
// 供主项目做错误归因展示（A8）。
type RegistrationFailedData struct {
	StatusCode int
	Hint       string // SIPErrorHints 映射的提示
	Attempt    int
}

// Event：单一事件通道投递的事件（H5：消灭多通道）。
type Event struct {
	Type   EventType
	At     time.Time
	Module string // 相关模块名，可空
	Reason string // 人类可读的原因
	Data   any    // 业务负载，类型由 Type 决定（见上方 *Data 结构）
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
	// Encoding 是短信编码："auto"（默认，按内容自动选择）或 "ucs2"（强制 UCS2）。
	// 中文/emoji 必须用 UCS2；纯 ASCII 用 auto 即可（GSM7 更省字节）。
	Encoding string
}

// SMSDeliveryStatus 是短信投递状态。
type SMSDeliveryStatus int

const (
	SMSStatusQueued    SMSDeliveryStatus = iota // 已入队
	SMSStatusSending                            // 发送中
	SMSStatusSent                               // 已发送（等待回执）
	SMSStatusDelivered                          // 已投递
	SMSStatusFailed                             // 失败
)

// SMSDeliveryRecord 是短信投递记录。
type SMSDeliveryRecord struct {
	MessageID string
	To        string
	Status    SMSDeliveryStatus
	Attempts  int
	At        time.Time
	Error     string
}

// SMSDeliveryStore 是短信投递存储接口（主项目实现持久化，如 SQLite）。
// 不实现时库用内存存储（进程重启丢失）。
type SMSDeliveryStore interface {
	Save(ctx context.Context, rec SMSDeliveryRecord) error
	Get(ctx context.Context, messageID string) (SMSDeliveryRecord, error)
	UpdateStatus(ctx context.Context, messageID string, status SMSDeliveryStatus, errMsg string) error
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
	errSMSNoSIP       = errors.New("ims: SMS 需要 SIP 栈（internal）")
	errUSSDNoSIP      = errors.New("ims: USSD 需要 SIP 栈（internal）")
)

// ErrSyncFailure 表示 AKA 同步失败（AUTN 序列号失步）。
// 对标 vowifi-core engine/sim.ErrSyncFailure，供迁移使用。
// 检测：errors.Is(err, ims.ErrSyncFailure)。
var ErrSyncFailure = sim.ErrSyncFailure

// ==================== SIP 错误码归因（A8） ====================

// SIPErrorHints 将 SIP 错误码映射到参数嫌疑提示（A8）。
// 供主项目在运营商调参时展示"这次失败最可能是什么参数的问题"，
// 配合 DecisionRecord 做差异分析，避免盲目排列组合。
var SIPErrorHints = map[int]string{
	400: "请求格式错误：检查 SIP 头完整性、Contact 格式",
	401: "需要认证：检查 AKA 配置、IMPI/IMPU 是否正确",
	403: "认证失败或被拒绝：检查 AKA 偏好、IMPI、代理配置；可能是运营商侧阻断",
	404: "用户不存在：检查 IMPU 格式、归属域是否正确",
	408: "请求超时：检查 P-CSCF 地址可达性、网络连通性",
	480: "临时不可用：对端忙或网络问题，稍后重试",
	486: "忙：对端正在通话中",
	488: "媒体协商失败：检查 SDP、编解码配置、IPsec 媒体保护设置",
	494: "安全协商失败：检查 IPsec 开关、Security-Client 头",
	500: "服务器内部错误：P-CSCF 侧问题，检查 P-CSCF 地址是否正确",
	503: "服务不可用：P-CSCF 过载或维护，尝试下一个 P-CSCF 候选",
}

// SIPErrorHint 返回指定状态码的归因提示；无映射时返回空字符串。
func SIPErrorHint(statusCode int) string {
	return SIPErrorHints[statusCode]
}

// ==================== Redaction ====================

// Redactor 是脱敏器（规则可注入，整改 go 写死正则）。
type Redactor struct {
	rules []RedactRule
}

// RedactRule 是一条脱敏规则。
type RedactRule struct {
	// Name 是规则名（诊断用）。
	Name string
	// Pattern 是匹配模式。
	Pattern *regexp.Regexp
	// Replace 是替换模板（可用 $1 等分组）。
	Replace string
}
