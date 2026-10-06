# WS-5 Register 蒸馏笔记

> 方法：双源对照 + 鼓励创造。目标：接近完美的注册实现，不是两版折中。
> vowifi-core：4980 行，用户生产验证（多模组多设备线上在跑）
> vowifi-go：6412 行，他人生产验证
> ims-go 现状：904 行，简化版

## 一、能力对照表

| 能力 | vowifi-core（生产） | vowifi-go（生产） | 取舍 |
|---|---|---|---|
| 初始 REGISTER 构造 | 变体矩阵（5+1 种花样） | 头集合完整（手拼） | core 的变体 + go 的头，sipgo 重写 |
| 401/407 AKA | 3 轮 + nonce 指纹 + AUTS | 3 轮上限 | core（防重放更完整） |
| EAP 直接认证 | ✅ 复用 SWu RES | ❌ | core（SWu→IMS 衔接关键） |
| 失败策略 | 换花样/换 registrar | 智能重试（135 行） | 两者：二维矩阵 |
| 200 OK 后处理 | P-A-URI→MSISDN | Expires/Route/GRUU 全 | 合并：MSISDN 用 core，其余用 go |
| 刷新语义 | 80% + protected REGISTER | 复用 session | core（更积极更轻） |
| 头排序 | Vodafone 白名单 | 无 | core |
| GRUU | 弱 | 完整 | go |
| Emergency | 待确认 | ;sos/匿名 | go（待核 core） |

## 二、vowifi-core 深读核心发现

**"57行为什么够用"**：vowifi-core 用"换花样重试"而非"退避重试"。生产洞察：IMS 注册失败 90% 是"请求格式不对"（换变体解决）或"P-CSCF 挂了"（换台解决），只有 10% 需要等。57 行抓住了主要矛盾。

**三个生产精髓**（ims-go 全缺）：
1. **EAP 直接认证**：复用 SWu 阶段 EAP-AKA 的 RES 做 SIP Digest，避免 USIM SQN 消耗两次导致同步失败——SWu→IMS 衔接关键
2. **变体矩阵**：5+1 种初始 REGISTER 花样（不同 auth/PANI/cellular 组合），按运营商试错
3. **Vodafone 头排序**：某运营商 P-CSCF 对 SIP 头顺序敏感，白名单顺序序列化

**关键机制**：
- AKA：3 轮上限 + nonce 指纹防重放 + AUTS 重同步
- 200 OK：从 P-Associated-URI 提 MSISDN（MVNO 卡 EF 常空，比 USIM 可靠）
- 刷新：expires×80% + protected REGISTER（非完整重注册）；401 触发重注册
- 传输：`reachedAuth` 后停止换 P-CSCF（认证失败是凭证问题）
- Plain 模式保留 TCP 给 SMS/USSD

**蒸馏优先级**：变体矩阵 → EAP 直接认证 → 二维失败决策 → 刷新语义修正 → AKA 完善 → Vodafone 排序 → GRUU（vowifi-go 有）

## 三、vowifi-go 深读（已有审计结论）

见 `docs/AUDIT_PHASE1.md` WS-5 节。核心缺失清单已记录。

## 四、逐能力取舍（待双源齐后填写）

## 五、超越想法
1. **二维失败决策矩阵**：换花样（vowifi-core）× 延迟重试（vowifi-go）——两个维度正交，组合使用
2. **变体学习**：用 P1 推导引擎记住"这运营商上次用变体 3 成功"，下次直接用，O(n)→O(1)
3. **EAP-SIP 密钥连续性**：CK/IK 直接做 IPsec 密钥，省一轮 AKA
4. **分层失败处理**：协议级智能重试 → 换变体 → 换 registrar，三级流水线

## 七、初步洞察（我亲自读 vowifi-core 失败处理）

**哲学差异**：
- vowifi-core（57 行）：**快速故障转移**哲学。失败→换下一个 registrar/variant。配置驱动（`InitialRejectFallbackStatusCodes` 可配），不纠结单个服务器。
- vowifi-go（135 行）：**智能重试**哲学。Retry-After、423、305、退避——跟单个服务器死磕到底。

**超越想法**：两者互补不互斥。理想是分层：
1. 先用 vowifi-go 的协议正确性（Retry-After/423/305 是 RFC 要求，必须做）
2. 再用 vowifi-core 的配置驱动故障转移（operator 可调，比硬编码灵活）
3. 顺序：协议级重试 → 仍失败 → 换候选。不是二选一，是流水线。
