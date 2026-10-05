# 互操作矩阵（WS-17）

> 替代 strongSwan 的互操作置信度基线。持续维护。

## 测试床

| 组件 | 实现 | 说明 |
|------|------|------|
| fake P-CSCF | `internal/sip/register` 测试 | sipgo server，401→200 |
| fake ePDG | `internal/swu` 测试 | IKEv2 fake peer + milenage 真 AKA |
| 回环 guard | `requireLoopbackUDP` | 沙箱跳过，CI 真跑 |

## e2e 链路（已验证）

- [x] REGISTER 401 → Digest-AKA → 200（`internal/sip/register`）
- [x] SUBSCRIBE → NOTIFY reginfo（`internal/sip/subscribe`）
- [x] MO SMS 发送（`internal/sip/sms`）
- [x] USSD 会话（`internal/sip/ussd`）
- [x] Voice Dial → 180/200（`internal/voice`）
- [x] RTP 中继 + PT 映射（`internal/voice/media`，CI 真跑）
- [x] Entitlement challenge 循环（`internal/entitlement`）

## 故障注入（已验证）

| 故障 | 预期行为 | 状态 |
|------|----------|------|
| TCP EOF | OnConnectionLost → 触发恢复 | ✅ WS-6 |
| 503 | P-CSCF 切换 + penalty | ✅ WS-5 |
| Challenge 超限 | 返回错误，不无限循环 | ✅ WS-14 |
| 保活失败 | 连续失败阈值 → 恢复回调 | ✅ WS-10 |

## 运营商实测清单（待补充）

| 运营商 | MCC/MNC | ePDG | VoWiFi | e911 | 备注 |
|--------|---------|------|--------|------|------|
| T-Mobile US | 310/260 | preset | - | - | 待实测 |
| O2 UK | 234/10 | preset | - | - | 待实测 |
| China Mobile | 460/00 | preset | - | - | 待实测 |

## 已知限制

1. SRTP/SDES 未实现（可选，产品需要时立项）。
2. 紧急呼叫默认禁用（需显式 opt-in）。
3. 沙箱环境回环 UDP 被拦截，相关测试在 CI 真跑。
