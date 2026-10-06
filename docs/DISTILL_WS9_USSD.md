# WS-9 蒸馏笔记：USSD over IMS（3GPP TS 24.390）

## 根本问题

当前 ims-go 用 **SIP MESSAGE** 发 USSD，这是错的。
vowifi-core（525行 ussd_transport.go）和 vowifi-go（1764行 ussi/）**两边一致**：
USSD over IMS = **INVITE 建 dialog → INFO 交互 → BYE 结束**（TS 24.390）。

## 双源对照

| 能力 | vowifi-core | vowifi-go | 取舍 |
|------|-------------|-----------|------|
| 模型 | INVITE→INFO→BYE | INVITE→INFO→BYE | 两边一致，采用 |
| INVITE Request-URI | `sip:<cmd>;phone-context=<domain>@<domain>;user=dialstring` | 同左 | 采用 |
| INVITE body | multipart (SDP + USSD XML) | 同左，boundary=`vohive_ussd` | 采用 |
| INFO 头 | Info-Package/Recv-Info/Content-Disposition | 同左 | 采用 |
| 单活动会话 | map 管理 | Service 只持有一个 | 取 vowifi-go：更简单，USSD 本来就是串行的 |
| 结果等待 | 同步等 200 | 200 可能直接带 USSD，否则等入站 INFO/BYE（ResultCh） | 取 vowifi-go：网络行为两种都有 |
| 菜单检测 | 无 | LooksLikeMenu（≥2 个编号选项）→ Status=1 | 取 vowifi-go |
| 入站 INFO/BYE | 有 | 有（Call-ID 匹配） | 取 vowifi-go |
| 会话超时 | 有 | 有（45s 事务超时） | 采用 |
| XML 信封 | ussd-data/xmlns/language/ussd-string | 同左 | 已有，保留 |

## 创造点

1. **ims-go 架构适配**：vowifi-go 依赖 `imsendpoint.ClientDialogEndpoint`（自研 dialog 层）；
   ims-go 用 sipgo 原生 + `internal/sip/dialog`。INVITE 用 sipgo transaction，
   dialog 状态用 `dialog.Registry` 跟踪，不引入第二套 dialog 机制。
2. **错误码语义**：Result.Status 统一：0=完成，1=需继续（菜单），2=失败，5=超时。

## 不取的部分

- vowifi-go 的 `imscore.Service` 耦合（common.RandomHex、sipkit、logging）：ims-go 有自己的工具链。
- vowifi-core 的 `messaging.USSDTransport` 接口：那是 runtimehost 层的，ims-go 的 `ims` 包自己定义。

## 实现计划

重写 `internal/sip/ussd/`：
- `types.go`：Session（CallID/RemoteTarget/State/ResultCh）、Service（单会话）、Config、Result
- `xml.go`：Encode/Decode（已有，保留）、LooksLikeMenu、ParseResult
- `sip.go`：BuildInitialInvite（multipart）、BuildInfo、BuildBye
- `service.go`：Send（INVITE→等结果）、Continue（INFO）、Cancel（BYE）
- `inbound.go`：HandleInboundInfo/Bye（Call-ID 匹配，供 inbound.Dispatcher 注册）

## 测试

- XML 编解码（保留现有）
- LooksLikeMenu 菜单检测
- multipart 构造/解析
- fake P-CSCF：INVITE→200（带 SDP+XML）→INFO→200→BYE 全流程
