# WS-6/10 蒸馏笔记：SIP 协议栈集成

## 问题

审计发现：`transport.Pipeline`、`dialog.Registry`、`keepalive.Keeper`、
`inbound.Dispatcher` 都是零集成的死代码——有实现，无装配。
`ims.Client` 的 `Modules.SIP` 槽位存在，但没有默认实现，
消费方需手工组装 6 个组件。

## 方案

新建 `internal/sip/stack` 包，一次装配：

```
Stack
├── sipgo UA (Client + Server)
├── transport.Pipeline（传输）
├── dialog.Registry（dialog 跟踪）
├── inbound.Dispatcher（入站分发，Register 到 Server）
├── register.Registrar（IMS REGISTER）
├── subscribe.Subscriber（SUBSCRIBE reg）
└── keepalive.Keeper（OPTIONS 保活，失败回调）
```

`Start` 顺序：传输 → REGISTER → SUBSCRIBE → keepalive。
`Stop` 逆序：keepalive → Unsubscribe → Pipeline.Close。

## ims 包集成

- `ims.SIPConfig` 从空 struct 补全：IMPU/IMPI/HomeDomain/PCSCFAddrs/
  Contact/RegisterExpires/SubscribeExpires/EAPRES。
- `ims/sip.go`：`newDefaultSIP`（仿 `newDefaultTunnel` 模式），
  AKA 与 SWu 共用（D-015）。
- `ims.New`：`Modules.SIP == nil && sipConfigured` 时自动装配。

## 不做的

- SMS/USSD/Voice 的默认装配：它们依赖 SIP 栈的 Client/Server，
  需要 Stack 暴露访问器，P2 再做（避免本次范围膨胀）。
- keepalive 失败 → 自动重注册：已留 TODO，联动 WS-5，P2。
