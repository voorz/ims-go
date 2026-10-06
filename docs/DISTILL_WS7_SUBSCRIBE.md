# WS-7 蒸馏笔记：SUBSCRIBE/NOTIFY

## 双源对照

| 能力 | vowifi-core (193行) | vowifi-go (551行) | ims-go 取舍 |
|------|---------------------|-------------------|--------------|
| SUBSCRIBE(reg) | ✅ 最小实现，生产可用 | ✅ 完整实现 | 取 vowifi-go 的完整性 |
| 独立 Call-ID | ✅ | ✅ | 已有 |
| Security-Verify 继承 | ✅ | ✅ | 已有 |
| CSeq 递增写入 | ❌ 未验证 | ✅ | **P0 修复**：之前递增但从未写入 |
| 接受 202 | ❌ 只认 200 | ✅ | **P0 修复**：200/202 都接受 |
| Subscription-State 解析 | ❌ | ✅ | **P0 修复**：检测 terminated |
| NOTIFY dialog 验证 | ❌ | ✅ (Call-ID+tag) | **P0 修复**：Call-ID 匹配，不匹配回 481 |
| 481 重建 | ❌ | ✅ | **P1 修复**：自动重建（上限 2 次） |
| 403/405/489 永久拒绝 | ❌ | ✅ | **P1 修复**：不重试 |
| 刷新间隔 | 固定 | expires-30s | **P1 修复**：改用 expires-30s |
| MWI 订阅 (message-summary) | ❌ | ✅ 独立订阅 | **延后**：P2。core 不做也能生产跑 |
| MWI 解析 | ❌ | ✅ (yes/1/true + Voice-Message) | **已取**：yes/1/true + new/old 计数 |
| NOTIFY terminated 自动重订 | ❌ | ✅ | **P1 修复**：StateActive 时自动重订 |
| reginfo XML 解析 | ❌ (只回 200) | ✅ | 已有 |

## 创造点

1. **统一 Subscription-State 解析**：`parseSubscriptionStateHeader` 同时服务请求和响应，避免重复代码。
2. **背压队列丢弃策略**：满时丢最旧（NOTIFY 是状态同步，新者覆盖旧者）——比 vowifi-go 的简单丢弃更合理。

## 不取的部分

- **独立 MWI SUBSCRIBE**：vowifi-core 不做也能生产跑；MWI 通过 reg NOTIFY 的 body 附带即可。P2 增强，不阻塞。
- **Timer N / 完整 dialog 状态机**：当前单订阅场景不需要，增加复杂度。

## 测试

- 现有测试通过（subscribe_test.go）
- P0 修复需要补充测试：CSeq 单调性、202 接受、481 重建、NOTIFY Call-ID 验证
