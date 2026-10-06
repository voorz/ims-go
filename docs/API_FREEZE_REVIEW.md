# ims-go 公开 API 冻结评审材料

> 生成日期：2026-10-06。`ims` 是唯一公开包（D-010，门禁④强制）。
> 以下为 `go doc ./ims` 输出的完整公开 API 表面。

## 设计原则

- **单入口**：`New(Config) (*Client, error)`，一次构造、一直使用。
- **生命周期收回**：Client 内部管理模块 Start/Stop、期望态对账（H2）、恢复策略（H3）。
- **可替换性**：子系统经 `Config.Modules` 以 interface 注入（D-010，P3）；
  未注入且已配置时自动装配默认实现（SWu、SIP）。
- **强类型**：无 `map[string]interface{}`（门禁②），导出类型集中 `types.go`（门禁③）。

## 公开类型清单

| 类型 | 说明 | 状态 |
|------|------|------|
| `Client` | 主客户端 | ✅ 稳定 |
| `Config` | 总配置 | ✅ 稳定 |
| `SIPConfig` | SIP 协议栈配置（WS-6/10 补全） | ✅ 本次新增字段 |
| `SWuConfig` | SWu/IKEv2 配置 | ✅ 稳定 |
| `SIMConfig` / `AKAProvider` / `SoftSIMConfig` | SIM/AKA（D-015） | ✅ 稳定 |
| `SMSModule` / `SMSRequest` / `SMSResult` | 短信 | ✅ 稳定 |
| `USSDModule` / `USSDResult` | USSD | ✅ 稳定 |
| `VoiceModule` / `VoiceControl` / `Call` / `CallRequest` | 语音 | ⚠️ 需评审（本次新增 Hold/Refer 方法） |
| `Modules` / `Module` | 模块注入点 | ✅ 稳定 |
| `Event` / `EventType` / `EventHandler` | 事件通道（WS-16） | ✅ 稳定 |
| `DecisionRecord` | P2 决策记录 | ✅ 稳定 |
| `Redactor` / `RedactRule` | 脱敏（WS-16） | ✅ 稳定 |
| `CarrierConfig` | 运营商配置（WS-13） | ✅ 稳定 |
| `DataplaneConfig` / `DataplaneMode` | 数据面（D-013） | ✅ 稳定 |

## 待用户确认

1. `VoiceConfig` 仍为空 struct——语音编解码偏好等是否需要公开配置？
2. `CarrierConfig` 仍为空 struct——WS-13 的 CarrierConfig 是否应公开？
3. `Call` 是否应包含本次新增的 dialog 字段（CallID/tags）？当前已公开。

## 附：完整 go doc 输出

```
package ims // import "github.com/voorz/ims-go/ims"

Package ims 是 ims-go 唯一对外公开的包（D-010）。

对外只暴露：New(Config) (*Client, error)。Client 一次构造、一直使用，
内部收回生命周期管理、期望态对账、恢复策略（H2/H3）、入站桥接（H1）、 启动前协议准备（H9）。实现细节全部位于 internal/，不得对外泄露。

本包在 WS-1 实现。

var ErrAlreadyRunning = errors.New("ims: 客户端已在运行") ...
var DefaultRedactor = NewRedactor()
type AKAProvider interface{ ... }
type AKAResult struct{ ... }
type Call struct{ ... }
type CallRequest struct{ ... }
type CarrierConfig struct{}
type Client struct{ ... }
    func New(cfg Config) (*Client, error)
type ClientState string
    const StateStopped ClientState = "stopped" ...
type Config struct{ ... }
type ConfigError struct{ ... }
type DataplaneConfig struct{ ... }
type DataplaneMode string
    const DataplaneUserspace DataplaneMode = "userspace" ...
type DecisionRecord struct{ ... }
type Event struct{ ... }
type EventHandler func(Event)
type EventType string
    const EventClientStarted EventType = "client.started" ...
type LoggingConfig struct{ ... }
type MilenageKeys struct{ ... }
    func CustomTestKeys(k, op []byte, useOPc bool) (MilenageKeys, error)
    func TestKeys() MilenageKeys
type Module interface{ ... }
type ModuleStatus struct{ ... }
type Modules struct{ ... }
type RecoveryPolicy struct{ ... }
type RedactRule struct{ ... }
type Redactor struct{ ... }
    func NewRedactor(extra ...RedactRule) *Redactor
type SIMConfig struct{ ... }
type SIPConfig struct{ ... }
type SMSModule interface{ ... }
type SMSRequest struct{ ... }
type SMSResult struct{ ... }
type SWuConfig struct{ ... }
type SoftSIMConfig struct{ ... }
type Status struct{ ... }
type USSDModule interface{ ... }
type USSDResult struct{ ... }
type VoiceConfig struct{}
type VoiceControl struct{ ... }
type VoiceModule interface{ ... }
```
