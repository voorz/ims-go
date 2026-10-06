# 语音蒸馏笔记（WS-11）：vowifi-go vs ims-go

> 日期：2026-10-06
> 方法：深读 vowifi-go 生产验证实现（~128 文件），对比 ims-go 简化实现，逐能力取最优 + 创造。
> 结论前置：vowifi-go 的语音是**真正生产验证过的重型引擎**（Agent per-device + 8 状态机 + Actor 序列化 + 双腿桥接 + RFC 3312/3262/4028 全套），ims-go 当前是**骨架级简化实现**（状态机+Actor 形似，但信令语义大量缺失）。差距不在架构形状，而在**信令细节的完整度**。

---

## 1. Agent 架构（per-device 模型）

### vowifi-go 实现方式
- `Agent{deviceID, ims, endpoint, bus, gateway, actor, dialog, clientBridge, calls map}`，per-device 单例（D-014 已采纳此模型）。
- **双层 Actor**：`callstate.Actor`（agent 级，bounded queue）+ 每 call 一个 actor。`notifyIMSEvent` 先尝试入 call actor 队列，失败再入 agent actor 队列，再失败则**同步直投 + rate-limited warn 日志**——三级降级保证事件不丢。
- `calls map` 支持**多别名注册**：`registerLiveCallLocked(call, false)`，同一 Call 可按 Call-ID/clientCallID/IMS Call-ID 多 key 索引；`activeCall` + `waitingCall` 双指针（呼叫等待）。
- `finalizeActiveCall`：`claimTerminalFinalization()` 做**幂等认领**（CAS），防止并发路径重复释放；释放后 `promoteLiveCallLocked()` 自动把 waitingCall 提升为 activeCall。
- EventBus 发布 `EventCallRinging/Answered/Ended/Failed/Canceled/MediaUpdated/IncomingCall/Waiting/Finalized` 全套事件；`EventCallFinalized` 携带 PCAP/录音路径（可观测性闭环）。

### ims-go 当前实现
- `Agent{cfg, log, calls map[string]*callActor}`，per-device + per-call Actor 形似 D-014。
- **缺**：bounded queue 拒绝处理（channel 16 无满时策略，`do()` 在 done 关闭后静默丢弃）、无 waitingCall、无多别名索引、无幂等认领、无 EventBus（只有 OnStateChange 回调）。

### 优点 / 坑
- vowifi-go 优点：三级事件降级 + 幂等认领是生产血泪（并发 CANCEL/BYE/超时三路竞争释放同一 call 是 VoWiFi 最高频 crash 来源）。
- vowifi-go 坑：`Snapshot()` 返回 `map[string]interface{}`（红线②风格问题）；`Agent` 字段过多（ims/endpoint/bus/gateway 四重身份，D-010 后应收敛为单一 endpoint）。
- ims-go 坑：`callActor.ch` 无缓冲满时阻塞、`do()` 在 close 后 select 会 panic（向已关闭 channel 发送）——`Close()` 直接 `close(ca.ch)`，若仍有 `do()` 在发送则 panic。

### 超越思路
- Actor 队列改用**带 context 取消的提交语义**：`do(ctx, fn) error`，调用方感知背压，而不是静默丢弃或阻塞。vowifi-go 的三级降级可简化为两级（actor 队列 → 同步直投），去掉中间层。
- `claimTerminalFinalization` 的 CAS 幂等思想保留，但用 `sync.Once` 语义封装为 `call.finalizeOnce.Do(...)` 更直白。

---

## 2. 呼叫生命周期（Dial / Hangup / 定时器）

### vowifi-go 实现方式
- `DialContext`：校验 registered → `startOutboundCall`（建 Call + TransitionChecked(Calling) + busy 检查 `cannotAddCallLocked`）→ `executeOutboundCall`：准备 SDP（含 precondition）→ `buildIMSInviteWithSDPChecked` → **启动 30s no-answer timer** → 发 INVITE → 按最终响应分流。
- `completeOutboundInvite`：`call.learnVoiceDialog(response)`（从 2xx 学 dialog），**2xx 才发 ACK**（注释明确：非 2xx 的 ACK 由事务层发，避免重复 ACK——这是 RFC 3261 细节，错了会导致对端收双 ACK）；`MarkACKSent()`；`applyVoiceSessionExpires`。
- `hangupCall`：按方向/状态分流——inbound 未接通 → 486 拒绝；outbound 未接通 → CANCEL（`cancelVoiceClientInvite` 走 invite handle）；已接通 → dialog 内 BYE → `closeCallDialog` → `finishLocalHangup`。
- **Late-accepted INVITE**（CANCEL 竞争）：`closeLateAcceptedInvite`——本地已 CANCEL 但对端回了 2xx → 必须先 ACK 再 BYE，否则 dialog 泄漏。这是生产必现的竞态，vowifi-go 显式处理。
- No-answer timer（60s，`StartOutboundNoAnswerTimerCurrent`）：超时只在 Calling/Ringing 才触发 → `handleOutboundInviteNoAnswerTimeout`。

### ims-go 当前实现
- `Dial`：构造 INVITE（**SDP 硬编码** `m=audio 5004 RTP/AVP 0`，无 precondition、无 codec 协商）→ `transport.DoRequest` 同步等最终响应 → 按 180/183/200 简单分流。
- **缺**：`learnVoiceDialog`（Dial 200 后**不学习 Call-ID/tag/RemoteTarget**——但 `buildReInvite` 却用了 `call.CallID/LocalTag/RemoteTag/RemoteTarget`，这些字段 Dial 后全是空的，**Hold/REFER 实际发出去的是坏请求**）；无 late-accepted 处理；无 CANCEL 路径（Hangup 未接通时直接发 BYE，是错的）；ACK 未发送（注释"发送 ACK（简化）"但代码里没有）。
- `Hangup`：未区分 inbound/outbound、未区分接通/未接通，一律发 BYE。

### 优点 / 坑
- vowifi-go 优点：`learnVoiceDialog` + 状态分流 + late-accepted 是 dialog 正确性的三支柱。
- ims-go 现状是**不可用的**：Dial 成功后 dialog 信息为空，后续所有 dialog 内请求（Hold/REFER/BYE）都是坏的。这是 P0 缺口，不是"简化"。

### 超越思路
- `learnVoiceDialog` 应做成**不可绕过的构造步骤**：`Dial` 返回前必须经 `establishDialog(response)`，否则返回错误。把"学 dialog"从约定变成类型约束（未学 dialog 的 Call 无法构造 re-INVITE——可用 `*establishedCall` 包装类型实现）。
- No-answer timer 与 Session Timer 可统一为**call-scoped timer registry**（`map[timerKind]*time.Timer` + `stopAll()`），vowifi-go 是每个 timer 独立字段（noAnswerTimer/SessionTimer/prackTimer），容易漏停。

---

## 3. Dialog 信令处理

### vowifi-go 实现方式
- `dialog_controller`（`dialog.NewController`）：dialog 内请求经 controller 统一发，自动维护 CSeq、Route set、RemoteTarget。
- Early dialog 学习：`OnEarlyDialog` 回调存 `DialogHandle`；`handleIMSResponseCallback` 处理 1xx（学 To tag、**199 时 terminateEarlyDialog**——199 是 early dialog 终结信号，vowifi-go 显式处理）。
- `sendCallDialogRequestWithOptions`：从 `call.IMSDialog()` 取 handle，无 handle 直接报错（fail-fast，不构造坏请求）。
- 重传 2xx 的 ACK：`ackRetransmittedInvite`（`finalObserved` atomic 去重）——网络重传 2xx 时补 ACK，防止对端 BYE。

### ims-go 当前实现
- `internal/sip/dialog` 包存在（registry + CSeq + in-dialog builder），但 `voice` 包**没用它**——`buildReInvite` 手工拼 Call-ID/CSeq/From/To，且 CSeq 递增与 dialog 包的 atomic CSeq 是两套。
- 无 early dialog 概念，无 199 处理，无重传 2xx 补 ACK。

### 优点 / 坑
- vowifi-go 优点：dialog 操作全部经 controller，handle 为空 fail-fast。
- ims-go 坑：两套 CSeq（dialog 包的 + call.CSeq 手工++）必然错乱；voice 与 dialog 包**零集成**（WS-6/10 审计已指出 Pipeline 未接入，此处是 voice 版）。

### 超越思路
- voice 的所有 dialog 内请求**必须经 `internal/sip/dialog` 包**，删除 `buildReInvite` 手工拼装。Dialog 信息（Call-ID/tags/RemoteTarget/route-set/CSeq）由 dialog 包拥有，voice 只持有 `*dialog.Dialog` 句柄。这是 D-007 单一映射原则在运行时的体现：信令状态单点拥有。

---

## 4. Hold / Resume（TS 24.610）

### vowifi-go 实现方式（`hold.go`）
- `setCallHold`：校验 Connected → `localHoldValue()==hold` 则直接返回（幂等）→ `sendLocalHoldOffer`。
- `sendLocalHoldOffer`：
  1. SDP 方向改写（sendonly/sendrecv）+ **`bumpSDPOriginVersion`**（o= 行版本递增，RFC 3264 要求）+ **`advertiseEstablishedSessionQoS`**（precondition 的 `a=curr` 重置为 established——hold 的 re-INVITE 必须重声明 QoS 状态，否则对端可能重新进 precondition 等待）。
  2. 经 `sendCallDialogInvite` 发 re-INVITE（带 provisional 回调，处理 1xx）。
  3. 非 2xx → 发 ACK（4xx-6xx 的 ACK）再返回错误。
  4. 2xx → ACK → `MarkACKSent` → **重学 Session-Expires/Min-SE**（re-INVITE 可能重协商 timer）→ `setLocalHold` → `setLocalSDP` → **`applyCallMediaDirection`**（按 SDP 方向 `relay.SetSendEnabled(...)`——hold 时停发 RTP，省电+避免对端收静音包）→ **`startVoiceSessionTimer`**（重启 timer）→ 发 `EventCallMediaUpdated`。
- 远端 hold：`setRemoteHold` + `Held() = localHold || remoteHold`（任一 hold 即显示保持中）。

### ims-go 当前实现（`supplementary.go`）
- `setHold`：校验 Connected → 状态相同直接返回 → `buildReInvite`（手工拼）→ 发 → 200 则 `call.LocalHold = hold`。
- **缺**：o= 版本递增、无 QoS 重声明、**无媒体方向联动**（relay 继续发 RTP，对端 hold 后仍收包）、**无 Session Timer 重启**（hold 的 re-INVITE 会重置 timer，旧 timer 继续跑会误杀）、无 4xx ACK、无事件。

### 优点 / 坑
- vowifi-go 的 `applyCallMediaDirection` 是生产关键：hold 后不停发 RTP 会导致对端计费/录音异常。
- ims-go 的 `startSessionTimer` 在刷新时调用 `setHold`（见下节）是**方向反了**的 bug。

### 超越思路
- Hold 的副作用链（SDP→信令→媒体→timer→事件）应做成**显式的 pipeline**：`holdPipeline = [buildOffer, sendInvite, ackOn2xx, updateMedia, restartTimer, emitEvent]`，每步可测试、可插拔。vowifi-go 是写死的顺序调用，测试靠 restoration test 覆盖全链，改一处容易漏。

---

## 5. Session Timer（RFC 4028）

### vowifi-go 实现方式（`session_timer.go`，~300 行）
- **解析**：`parseSessionExpiresOffer` 解析 `1800;refresher=uac`，refresher 非法值丢弃；`applyVoiceSessionExpires` 非法值 warn 不 crash。
- **Min-SE**：`applySessionMinSE`，`SessionExpires < Min-SE` 时上调；**422 重试**：`sendSessionRefresh` 收到 422 → 解析 Min-SE → `applySessionMinSE` → 重发（`retried422` 防无限循环）。
- **IR.92 特例**：inbound 2xx 对端声明 `Supported: timer` 但省略 Session-Expires → fallback 1800（`applyInboundSessionTimer`）。
- **Refresher 方向感知**：`weAreSessionRefresher()`——outbound 看 refresher==uac，inbound 看 refresher==uas（RFC 4028 角色反转，很多人写错）。
- **刷新策略**：我是 refresher → `sessionRefreshDelay`（half ≥90s 用 half，否则 expires-10s）后发 **UPDATE**（`buildIMSSessionUpdate`）；UPDATE 被 405/501 拒绝 → 降级 re-INVITE（`sendSessionRefresh(useInvite=true)`）。刷新成功 → **重启 timer**（`sendIMSSessionUpdate` 末尾调 `startVoiceSessionTimer`）。
- **对端是 refresher**：expires 到时直接 `expireVoiceSession` → Hangup（死呼叫最后防线，不无限等）。
- Hold 的 re-INVITE 响应重学 Session-Expires 并重启 timer（见上节）。

### ims-go 当前实现（`supplementary.go`）
- `startSessionTimer(call, expires, refresher)`：uac → expires/2 后调 `a.setHold(ctx, call.ID, call.LocalHold)`；uas → expires 后 Hangup。
- **P0 bug**：`setHold` 首行 `if call.LocalHold == hold { return }`——刷新时 hold 状态必然未变，**直接返回，根本没发任何刷新请求**。Session Timer 名存实亡。
- 无 Min-SE/422、无 UPDATE（注释"简化：用 re-INVITE 刷新"但实际没发）、无 refresher 方向感知（inbound 的 uas 角色写错会自己刷新+对端也刷新，双刷新）、无 IR.92 fallback（Dial 里写了 1800 fallback 但只在 200 OK 无头时，且写死 uac）。

### 优点 / 坑
- vowifi-go 的 UPDATE-first + re-INVITE-fallback 是运营商兼容性的关键（有些 P-CSCF 不支持 UPDATE）。
- ims-go 必须重写，当前实现是错的不是简化的。

### 超越思路
- vowifi-go 的 `sendSessionRefresh(ctx, call, useInvite, retried422)` 用布尔参数做状态机，可读性差。改为**显式 refresher 状态机**：`state: idle → updating → (422 → adjust → updating) → ok`，422 重试计数器代替 bool。
- 增加**可观测性**：每次刷新记录 `DecisionRecord`（P2 决策追溯），vowifi-go 只有 warn 日志。

---

## 6. PRACK（RFC 3262，100rel）

### vowifi-go 实现方式
- INVITE 声明 `Supported: 100rel`（`builder_transaction.go`）。
- `outbound_provisional.go`：1xx 响应若 `Require: 100rel` → 提 RSeq → `markReliableProvisionalRSeq` 去重 → `sendReliableProvisionalPRACK`。
- `prack_timer.go`：**T1 指数退避重传**（500ms → 4s 上限，64*T1=32s 总期限），`prackGeneration` 计数器防旧 timer 误触发，收到终结响应 `StopPrackTimer`。
- `early_dialog_runtime.go`：转发的 PRACK（双腿桥接场景）带 RAck 上下文。
- 183 带 SDP + precondition 时：先处理 SDP/更新 precondition 状态，再发 PRACK（顺序重要：PRACK 的 offer/answer 语义）。

### ims-go 当前实现
- **完全没有**。INVITE 不声明 100rel，1xx 直接按 180/183 分流，无 RSeq 提取，无 PRACK。

### 优点 / 坑
- 无 100rel 时，对端若坚持 `Require: 100rel` 会回 420 Bad Extension，呼叫直接失败。这是**运营商兼容性 P0**（部分欧洲运营商强制 100rel）。
- vowifi-go 坑：PRACK 重传与 sipgo 事务层重传可能双重（自建 `StartPrackRuntimeRetransmission` 是因为走 raw string 通道；ims-go 用 sipgo Client 发 PRACK 可依赖事务层重传，**不需要自建退避**）。

### 超越思路
- ims-go 用 sipgo 原生事务发 PRACK（D-012），删除自建退避 timer。PRACK 的 RAck 构造是唯一要手写的部分。
- Precondition 与 PRACK 联动：`a=des:qos` 的 SDP 必须在**可靠**的 183 里（RFC 3312 要求 precondition 的 offer/answer 用可靠传输），无 PRACK 则 precondition 语义不完整——两者必须一起上。

---

## 7. Precondition（RFC 3312 / IR.51）

### vowifi-go 实现方式
- Offer 构造（`builder.go:40`）：`Require: precondition` + SDP 插入 `a=curr:qos local sendrecv / remote none` + `a=des:qos mandatory local sendrecv / optional remote sendrecv`（`sdpQoSReservedLocal`，Wi-Fi 下本地资源恒可用）。
- 状态机有专用 `StatePreconditionWait`：`applyCallPreconditions` 按远端 SDP 的 curr/des 计算是否满足，不满足则 EarlyMedia→PreconditionWait，满足则回 EarlyMedia。
- `sdpPreconditionsSatisfied`：解析 curr/des 的方向掩码（send=1/recv=2/sendrecv=3），mandatory 项逐项校验——不是字符串包含，是**语义校验**。
- 远端 183 带新 SDP → `queuePreconditionStatusUpdate` → 发 **UPDATE** 报告本地状态（`outbound_precondition_update.go`），`claimPreconditionStatusUpdate` 防重复。
- re-INVITE（hold）时 `advertiseEstablishedSessionQoS` 把 `curr:qos remote none` 改写为 `remote sendrecv`（已建立会话不再走 precondition）。

### ims-go 当前实现
- **完全没有**。状态机里有 `StatePreconditionWait`（抄了形状），但无任何进入/退出路径（transitionMap 里 PreconditionWait 只能到 Connected/Terminating，但没人转入）。

### 优点 / 坑
- IR.51/Ir.92 要求 VoWiFi 信令带 precondition（即使 Wi-Fi 资源恒可用也要走形式），缺了会导致**部分运营商直接拒绝 INVITE**（420 或 488）。
- vowifi-go 坑：`ensureOriginatingPreconditions` 是字符串插入 SDP，对 SDP 结构假设强（m=audio 定位）；ims-go 有 SDP 对象模型（WS-12），应在其上实现。

### 超越思路
- 在 ims-go 的 **SDP 对象模型**上实现 precondition（`sdp.Media.Attributes` 增删 `curr`/`des`），而不是字符串拼接。这是"创造"的点：vowifi-go 的字符串操作是技术债，ims-go 的对象模型是优势。
- `StatePreconditionWait` 保留，但进入条件改为 `sdpPreconditionsSatisfied(remoteSDP)==false` 的显式判断（vowifi-go 已是此语义，ims-go 需补线）。

---

## 8. ECT / REFER（RFC 3515 / 3891）

### vowifi-go 实现方式
- `refer.go`（ blind transfer，未细读但存在）+ `ect.go`（consultative transfer，细读）：
  - `TransferConsultative(transfereeCallID, targetCallID)`：双 call 必须 Connected → `formatConsultativeReferTo` 构造 `Refer-To: <uri;method=INVITE?Replaces=callID%3Bto-tag%3D...%3Bfrom-tag%3D...>`（**token 逐字符百分号编码**，`escapeReferToToken`）。
  - 发 REFER → 等 202 → **`armReferSipfrag` 等 NOTIFY 的 sipfrag body** → 解析 `SIP/2.0 200` 首行 → 成功才挂断两路（`HangupContext` 双调）。
  - 403/420/501 明确映射为 `ErrECTRequiresReplaces`（语义化错误，调用方可降级为 blind transfer）。
- `replaces.go`：被叫侧处理带 Replaces 的 INVITE（join 逻辑）。

### ims-go 当前实现
- `Refer(ctx, callID, target)`：手工拼 REFER，Refer-To 直接用 target 字符串（**无 Replaces 构造**，即只支持 blind transfer 且连 blind 的 consultative 降级都没有），202/200 即返回成功，**不等 NOTIFY**（转移实际失败了也不知道）。

### 优点 / 坑
- vowifi-go 的 sipfrag 等待是 ECT 正确性的核心：202 只表示"收到转移请求"，转移成败在 NOTIFY 里。ims-go 当前是"发了就不管"。
- vowifi-go 坑：`waitReferSipfrag` 的 channel 等待与 call 释放竞争时可能泄漏（NOTIFY 来时 call 已挂断）。

### 超越思路
- REFER 统一为**三阶段状态机**：`referSent → accepted(202) → notified(sipfrag 2xx/failed)`，NOTIFY 超时（建议 30s）转失败。ims-go 的 Actor 模型适合做这个（vowifi-go 是 channel + ctx，ims-go 可用 actor 串行 + timer）。
- Blind vs consultative 做成**自动降级**：consultative 的 403/420/501 → 自动转 blind（Replaces 换普通 Refer-To），vowifi-go 只返回错误让调用方处理，ims-go 可内置降级。

---

## 9. 入站（Inbound）与双腿桥接

### vowifi-go 实现方式
- `inbound_runtime.go`：INVITE/PRACK/BYE/CANCEL 分流；`inbound_dialog.go` 建 inbound dialog；`inbound_forward.go` 双腿桥接（B2BUA）：入站腿 ↔ 出站腿的 SDP/媒体转发。
- `Answer` 明确拒绝无 SDP 的接听（`Answer(callID)` 返回 "inbound answer requires client SDP"——**防无媒体接通**，这是 vowifi-go 的生产教训）。
- Call waiting：`waitingCall` + `EventCallWaiting`，`promoteLiveCallLocked` 自动提升。

### ims-go 当前实现
- `Bridge` 实现 `inbound.VoiceRequestHandler`，`OnIncomingCall func(call *Call)` 回调。骨架在，无 SDP 校验、无 call waiting、无双腿桥接（`bridge/` 目录存在但未知完整度）。

### 超越思路
- 借鉴 vowifi-go 的"无 SDP 不接通"铁律：`Answer` 必须校验 SDP，否则返回错误。这是低成本高价值的生产经验。
- 双腿桥接的媒体转发应复用 WS-12 的 relay（vowifi-go 的 inbound_forward 是独立媒体路径，ims-go 可统一）。

---

## 10. 媒体联动（RTP relay 与呼叫状态）

### vowifi-go 实现方式
- `media_runtime.go`：每 call 一个 RTP relay（双 socket？）；`applyCallMediaDirection` 按 SDP 方向开关发送；hold 时 `SetSendEnabled(false)`。
- `call_lifecycle.go`：`startMediaResourcesCurrent`（relay.Start + comfort noise generator）、`stopMediaCurrent`（按序停：先停 comfort noise 再停 relay）。
- PCAP/音频抓包：`StartPCAP`/`captureResult`，`EventCallFinalized` 携带路径（问题定位闭环）。

### ims-go 当前实现
- `internal/voice/media` 有 RTP relay（**单向**，LAN→IMS，IMS→LAN 不转发——P0 缺口），无与呼叫状态的联动（hold 不停发）。

### 超越思路
- Relay 方向控制收归 **call 状态机**：`transition(Connected→Hold)` 自动触发 `relay.SetSendEnabled(false)`，而不是每个调用点手动调。状态机是单一事实来源。

---

## 总表：能力矩阵

| 能力 | vowifi-go | ims-go 现状 | 优先级 |
|---|---|---|---|
| per-device Agent + Actor | ✅ 双层 Actor + 三级降级 | ⚠️ 形似，无降级/幂等 | P1 |
| Dialog 学习（Call-ID/tags/Target） | ✅ learnVoiceDialog | ❌ Dial 后全空，Hold/REFER 发坏包 | **P0** |
| Early dialog / 199 | ✅ | ❌ | P1 |
| Late-accepted INVITE（CANCEL 竞态） | ✅ ACK+BYE | ❌ | P1 |
| 2xx 重传补 ACK | ✅ | ❌ | P2 |
| Hold（o= 递增/QoS/媒体联动/timer 重启） | ✅ 全套 | ⚠️ 只改 SDP 方向 | P1 |
| Session Timer（422/UPDATE/方向感知） | ✅ RFC 4028 全套 | ❌ 有 P0 bug（setHold 早退） | **P0** |
| PRACK（100rel） | ✅ T1 退避 | ❌ | **P0**（运营商兼容） |
| Precondition（RFC 3312） | ✅ 语义校验+UPDATE | ❌（状态机有空位） | **P0**（运营商兼容） |
| REFER blind | ✅ | ⚠️ 不等 NOTIFY | P1 |
| ECT consultative（Replaces+sipfrag） | ✅ | ❌ | P2 |
| Replaces（被叫侧） | ✅ | ❌ | P2 |
| Call waiting | ✅ | ❌ | P2 |
| 无 SDP 不接通 | ✅ 铁律 | ❌ | P1 |
| No-answer timer | ✅ 60s | ⚠️ Config 有字段，未接线 | P1 |
| 幂等释放（CAS） | ✅ | ❌（Close 有 panic 风险） | P1 |
| PCAP/录音 + Finalized 事件 | ✅ | ❌ | P2 |

## 蒸馏取舍建议（下一步）

1. **P0 先修（不做语音不可用）**：Dialog 学习 → Session Timer 重写 → PRACK+Precondition（两者联动，一起上）。
2. **P1 补齐**：Hold 全套副作用、REFER 等 NOTIFY、no-answer timer 接线、幂等释放、无 SDP 不接通。
3. **P2 增强**：ECT consultative、Replaces、call waiting、PCAP。
4. **创造点**（比两边好）：
   - Dialog 状态由 `internal/sip/dialog` 单点拥有，voice 只持句柄（删手工拼装）。
   - Precondition 在 SDP **对象模型**上实现，不用字符串拼接。
   - Timer 统一为 call-scoped registry；Session refresher 做成显式状态机。
   - REFER 三阶段状态机 + blind/consultative 自动降级。
   - Actor 提交语义带 context（`do(ctx, fn) error`），替代静默丢弃。
