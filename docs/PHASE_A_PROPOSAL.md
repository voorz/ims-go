# Phase A 方案：ims-go 迁移 API 缺口 + 语音前置条件

> 状态：待用户评审
> 日期：2026-10-06

## 背景

vohive-next 迁移需要 ims-go 补 6 个 API 缺口（A1/A3/A4/A5/A6/Proxy），另有语音后端前置条件（从未验证，需补齐才能谈"前半段铺好"）。

用户已拍板：
1. Proxy：保留场景，精简实现 + 可插拔
2. SMS 分片：砍掉，消息级够用
3. 运营商差异化：三层收敛 + 错误归因，不回老路
4. Gateway：不复制高级功能，后端先行

---

## Phase A1：迁移 API 缺口（6 项）

### A1. Event 业务负载

**现状**：`ims.Event` 只有 `Type/At/Module/Reason`，vowifi-core 的 `eventhost.Event` 缠业务数据。

**方案**：加 `Data any` 字段 + 强类型事件常量。

```go
type Event struct {
    Type   EventType
    At     time.Time
    Module string
    Reason string
    Data   any  // 新增：业务负载
}

// 强类型事件
const (
    EventSMSReceived EventType = "sms.received"
    EventSMSSent     EventType = "sms.sent"
    EventLocalNumberLearned EventType = "identity.local_number_learned"
)
```

**取舍**：用 `Data any` 而非强类型 struct——事件种类会增长，any + 类型断言最灵活；强类型事件常量保证可发现性。

**影响**：`internal/device/vowifi_dispatcher.go` 等 3 文件。

---

### A3. 入站呼叫契约

**现状**：`ims.IncomingCallHandler` 只有 `HandleIncomingCall(ctx, from, callID string) bool`，vowifi-core 有 `InboundCallRequest/Response`。

**方案**：新契约，携带完整呼叫上下文。

```go
type IncomingCallRequest struct {
    From       string
    CallID     string
    RemoteSDP  string
    Headers    map[string]string
    // 允许 handler 返回 provisional response
}

type IncomingCallResponse struct {
    Accept     bool
    StatusCode int    // 486/603 等拒绝码
    LocalSDP   string // 接受时的 SDP answer
}

type IncomingCallHandler interface {
    HandleIncomingCall(ctx context.Context, req IncomingCallRequest) IncomingCallResponse
}
```

**取舍**：不照抄 vowifi-core 的 `InboundCallRequest/Response`（字段冗余），按实际需要精简：From/CallID/RemoteSDP/Headers 足够；Response 支持拒绝码和 SDP answer。

**影响**：`VoiceConfig.OnIncomingCall` 签名变更。

---

### A4. PrepareStart 接入 ims.New

**现状**：`internal/identity.PrepareStart` 存在但未接入生产路径。

**方案**：`ims.New(Config)` 内部调用 `identity.PrepareStart`，做三件事：
1. Profile 校验（SIP/IMS 参数合法性）
2. Carrier 解析（调 `ResolveEffectiveCarrierConfig`）
3. Identity 三态（isim/auto/derived）

```go
func New(cfg Config) (*Client, error) {
    prepared, err := identity.PrepareStart(identity.PrepareStartInput{
        Profile: cfg.SIP,
        Carrier: cfg.Carrier,
        SIM:     cfg.SIM,
    })
    if err != nil { return nil, err }
    // ... 用 prepared 组装 Client
}
```

**取舍**：PrepareStart 保持 internal，对外只暴露错误。主项目不需要知道中间态。

---

### A5. CellID 注入

**现状**：REGISTER 的 PANI 是硬编码 `IEEE-802.11`，缺真实 CellID。

**方案**：`SIPConfig` 加 `CellID string` 字段，REGISTER 时注入 PANI 头：

```
P-Access-Network-Info: IEEE-802.11; i-wlan-node-id=<mac>
P-Access-Network-Info: 3GPP-UTRAN; utran-cell-id-3gpp=<cellid>
```

**取舍**：CellID 由主项目提供（从 modem 读），ims-go 只负责注入。不做自动获取——那是设备层的事。

---

### A6. Carrier 公开 API

**现状**：`internal/carrier` 全 internal，主项目运营商页面需要调用。

**方案**：公开最小集合：

```go
// 解析生效的运营商配置（用户覆盖 > 学习 > profile > 预设 > 推导）
func (c *Client) ResolveCarrier(plmn string) (CarrierConfig, error)

// 拉取云端 YAML profile 并本地保存
func FetchProfile(ctx context.Context, plmn string) (ProfileYAML, error)
```

**取舍**：不公开整个 `internal/carrier` 包（太大），只公开这两个主项目真正需要的入口。内部 resolver 保持 internal。

---

### A7. Proxy（新增，原 A1-A6 之外）

**现状**：vowifi-core 有 `ProxyConfig`（Addr/Host/Address 三字段冗余）+ 库内 SOCKS5。

**方案**：精简 + 可插拔。

```go
// 精简的公开配置（去重）
type ProxyConfig struct {
    Addr     string // host:port
    Username string
    Password string
    Enabled  bool
}

// 可插拔的传输工厂（主项目可注入自己的实现）
type SWuTransportFactory func(local, remote string) (Transport, error)

type SWuConfig struct {
    // ... 现有字段
    Proxy            *ProxyConfig        // 可选，nil = 直连
    TransportFactory SWuTransportFactory // 可选，覆盖默认
}
```

库内提供标准 SOCKS5 实现作为默认。

**取舍**：不抄 vowifi-core 的三字段冗余；TransportFactory 让主项目有逃生口（比如以后用其他代理协议）。

---

### A8. 错误码归因表（新增，支撑运营商调参）

**现状**：SIP 错误码无人翻译，调参靠猜。

**方案**：ims-go 内置错误码→参数嫌疑映射，主项目展示：

```go
var SIPErrorHints = map[int]string{
    403: "认证失败：检查 AKA 偏好、IMPI、代理配置",
    404: "域名/P-CSCF 不可达：检查 ePDG 派生、DNS",
    488: "媒体协商失败：检查 SDP、编解码配置",
    494: "安全协商失败：检查 IPsec 开关",
}
```

配合 P2 DecisionRecord，主项目可展示"尝试历史 → 差异分析"。

**取舍**：映射表放 ims-go（领域知识），展示逻辑放主项目（UI）。

---

## Phase A2：语音后端前置条件

> 目标："前半段铺好"——后端具备真实通话能力，前端以后再接。

### 诚实缺口清单（按优先级）

| # | 缺口 | 现状 | 方案 |
|---|---|---|---|
| 1 | PRACK 闭环 | 只有 `Supported: 100rel`，1xx 拦截回退 | 实现事务级 1xx 拦截 + PRACK 发送 |
| 2 | 入站 200/180/486 | `Answer` 只更新状态，有 TODO | 保存 inbound server transaction，真正回 200 OK |
| 3 | BYE/CANCEL 回调 | `IncomingCallHandler` 过于精简 | A3 新契约覆盖 |
| 4 | Voice agent 默认装配 | `mapVoiceConfig` 未被调用 | `ims.New` 内默认装配 |
| 5 | 公开 SetMediaAddr | internal 方法 | 公开 `Client.SetMediaAddr` |
| 6 | AMR/AMR-WB 编解码 | 无 | 引入编解码 + PCM↔RTP pipeline |
| 7 | 四 socket 真实验证 | 沙箱 skip，CI 未跑 | 正常环境验证 |

**取舍**：按 1→7 顺序做，每项独立可验证。不追求一次全做完，但每项做完都是真实可用的增量。

---

## 执行顺序

```
A1 (Event.Data) ──┐
A3 (入站呼叫)    │
A4 (PrepareStart) ├─→ 可并行，先做
A5 (CellID)      │
A6 (Carrier)    ──┘

A7 (Proxy) ──→ 独立，可并行
A8 (错误归因) ──→ 独立，可并行

A2 (语音) ──→ A1/A3 完成后开始（依赖新契约）
```

## 不做的事

- ❌ SMS 分片追踪（已砍）
- ❌ Gateway 高级功能复制（从未验证）
- ❌ 前端语音 UI（后半段）
- ❌ vowifi-core ProxyConfig 三字段冗余（精简版替代）
