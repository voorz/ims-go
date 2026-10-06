# 伙伴审计意见回复（2026-10-06）

## 第 2 条：sipgo 依赖不统一

**现状**：
- ims-go：`github.com/emiago/sipgo v1.6.0`（上游社区版，D-012 已定）
- vohive-libs：`github.com/voorz/sipgo`（fork 版）

**我的意见**：这是真实存在的生态风险，但分两层看。

1. **ims-go 内部**：一致，无问题。D-012 已验证 fork 只多 4 个 API，
   全部可用上游原生路径重实现，无需 fork。

2. **消费方（vohive-next）风险**：如果 vohive-next 同时依赖
   ims-go（上游 sipgo）和 vohive-libs（fork sipgo），Go 会将两者视为
   不同 module，`sipgo.Client` 类型不互通，编译期就会冲突。

**选项**：
- A（推荐）：vohive-libs 迁移到上游，与 D-012 对齐。fork 的 4 个 API
  已有重实现路径，迁移成本可控。
- B：接受双版本。消费方需做类型隔离，长期维护成本高。
- C：ims-go 改用 fork。违反 D-012，且 fork 长期不维护的风险更高。

**用户结论（2026-10-06）**：此问题可跳过。fork 与上游无架构区别，
fork 的改动只是为适配旧 vowifi-core 做的补丁，重构后大概率用不着。
ims-go 继续用上游 `emiago/sipgo`，无需处理。

## 第 5 条：VoiceConfig 和 CarrierConfig 为空

**现状**：`ims.VoiceConfig` 和 `ims.CarrierConfig` 都是空 struct，
但内部已有完整实现（`internal/voice.Config`、`internal/carrier.CarrierConfig`）。

**我的意见**：这是 WS-13/WS-11 的遗留——内部实现了，公开 API 没跟上。
按 D-007/D-010 模式，公开配置应是内部配置的**用户-facing 子集**，
通过 `ims/voice.go`、`ims/carrier.go` 做单处映射（仿 `ims/sip.go`）。

**提案**：

```go
// VoiceConfig：语音用户偏好（非内部 wiring）。
type VoiceConfig struct {
    Codecs   []string // 偏好编码，如 ["AMR-WB", "AMR"]；空用默认
    DTMFMode string   // "rfc4733"（默认）或 "inband"
    MaxCalls int      // 最大并发呼叫；0 用默认 2
}

// CarrierConfig：运营商档案覆盖（WS-13 内部模型的公开子集）。
type CarrierConfig struct {
    MCCMNC       string   // 如 "23415"；空则自动推导
    PCSCFAddrs   []string // 覆盖 P-CSCF；空则用 DNS/SRV
    // ... 其他按需
}
```

**需用户确认**：暴露哪些字段？我的提案是否覆盖实际需求？
