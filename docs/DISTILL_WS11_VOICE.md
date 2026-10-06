# WS-11 Voice 蒸馏笔记

> 方法：蒸馏四步法 ②。目标：vowifi-go 的完整度 + ims-go 的干净架构。
> vowifi-core 对照：其语音从未调通，无参考价值，不列入蒸馏来源。

## 一、架构对照

### vowifi-go voice 架构（85 个非测试文件）

```
Gateway（多设备注册表 + per-device 分发 worker）
  └── Agent（per-device）
        ├── callstate.Actor（单 goroutine 串行化）
        ├── dialog.Controller（注册头缓存 + CSeq 分配）
        ├── client.Bridge（本地客户端桥接）
        ├── calls map[string]*Call
        │     └── Call（嵌入 DialogState/MediaState/Timers）
        └── media.Manager（relay/rtcp/dtmf/monitor/pcap）
```

核心设计决策：
1. **Agent per-device**：多卡多设备场景，每个 device 独立 Agent
2. **Actor 串行化**：所有呼叫状态变更走单 goroutine，避免锁竞争；事件队列有界，拒绝时 fallback 直接投递（不静默丢）
3. **Gateway 分发**：按 deviceID 路由，有 epoch 生命周期管理
4. **Dialog 上下文**：从注册派生不可变头缓存（20+ 字段），非 registry

### ims-go 现状（4 个文件）
- Agent（单设备）+ 8 状态机（形状对）+ 基础桥接
- 缺：Gateway、dialog 头缓存、media.Manager、所有补充业务、定时器

## 二、分片深读（待子代理报告）

### 2.1 呼叫控制
**Actor 模式**：有界队列（128）、非阻塞 Enqueue（满则丢弃+限流告警）、panic 隔离（recover）。背压策略是 drop 不是 block。
**呼叫生命周期**：NoAnswer 定时器（60s）、PCAP 抓包、幂等 CloseDone（sync.Once）、cleanupOnce。Agent 停止用快照模式（锁内复制、锁外释放，防死锁）。Hangup 超时降级为 forceRelease。
**多呼叫**：maxConcurrentCalls=2、呼叫等待（30s 超时释放）、SwitchCall、active 终结后自动提升。
**Hold/Resume**（TS 24.610）：re-INVITE 改 SDP 方向（sendonly/sendrecv）+ relay.SetSendEnabled + 更新 session timer。localHold/remoteHold 双标志。
**Conference**（TS 24.605）：INVITE factory → REFER 两路 → SUBSCRIBE conference-info → 解析 RFC 4575 XML。
**Refer**（RFC 3515）：202 立即回 → 异步执行 → NOTIFY sipfrag；channel 模式（arm/wait/complete）带 ctx 超时。
**ECT**：REFER+Replaces（RFC 3986 转义）→ 等 NOTIFY sipfrag → 2xx 则挂断两路。
**Replaces**（RFC 3891）：callid+双向 tag 匹配；无匹配→481；early-only 但已 connected→486。
**Session Timer**（RFC 4028）：refresher 判定；我是 refresher→expires/2 发 UPDATE（失败只 warn 不杀呼叫）；对端是 refresher→超时直接 Hangup（死呼叫检测最后防线）；IR.92 特例（无 Session-Expires 头则 fallback 1800）。
**PRACK Timer**：T1 退避（500ms 起，倍增，上限 4s，总 32s）+ generation 防 stale。
**生产坑点**：10 项（见子代理报告：IR.92、hold 对齐、刷新失败不杀呼叫、Actor 队列满丢弃、清理幂等、快照释放、停机超时降级、Refer-Sub:false 等）。
**红线#1**：buildIMSRefer/buildIMSConferenceSubscribe 等是字符串拼接，必须重写为 sipgo 对象。

### 2.2 SDP 协商
**架构差异**：vowifi-go 用行处理（string lines），ims-go 用对象模型。蒸馏建议：offer 构造/改写用行处理（减少行为差异），解析保留对象模型做查询。SDP body 行处理不违反红线#1（针对 SIP 消息）。
**Precondition**（RFC 3312）：IR.51 QoS 行；位掩码检查 curr&des==des；UPDATE 全通话只发一次（claim）；联动状态机（PreconditionWait→EarlyMedia）；失败不拆通话。
**Codec**：offer 优先级 AMR-WB>AMR>telephone-event；answer 交集按远端顺序；PT 映射只处理动态 PT（≥96）；双 telephone-event（96/16000+101/8000）；PT 0/8 无 rtpmap 时硬编码 PCMU/PCMA。
**SDP 重写**（B2BUA 核心）：删 a=crypto（强制降级明文）、SAVP→AVP、c=/m=/o=/rtcp 改写、telephone-event 缺失时主动补全、客户端 a=crypto 透传。无 ICE（经 IPsec 隧道不需要）。
**降级哲学**：SDP 非法/UPDATE 失败/录音失败/无 telephone-event——一律记日志保通话，不拆线。
**生产坑点**：9 项（含 telephone-event fmtp 0-15 vs 0-16 不一致待确认、PRACK RSeq 去重、199 终结 early dialog 等）。

### 2.3 入站/出站/网关
**入站双路径**：同步分发器直调 + endpoint 事件队列（64 缓冲单 worker），OwnsInboundVoiceMethod 防重复消费。
**方法分发**：INVITE/BYE/CANCEL/ACK/PRACK/UPDATE/REFER/NOTIFY。BYE 先回 200 再处理（防重传风暴）；CANCEL 200 必须带 To tag；重传 INVITE 幂等（Ringing 中直接回 0）。
**入站 INVITE 链**：校验（CallID/From/To/SDP/Replaces）→ 建 call（忙则 486）→ 100 Trying 占事务 → dialog 上下文 → SDP（失败 488）→ History-Info → 180 → 定时器（等待 30s/无应答 120s）。
**双腿桥接**（B2BUA 核心）：向本地 client 转发 INVITE（独立 Call-ID、固定 UDP）→ 等终响 → 2xx 则接通 IMS；失败则推送 + 等上线重试（120s）。ims-go 的 Bridge 只是"通知业务层"，缺整套 B2BUA。
**re-INVITE**：非 Connected→491；SDP 重协商 + 对端 hold 检测（方向推导）+ 方向协商。
**UPDATE**：precondition/会话定时器两条路径；失败不杀呼叫。
**出站**：本地 client INVITE → 校验 → IMS INVITE；1xx 处理（199 终结 early dialog、PRACK 去重、precondition UPDATE）；2xx 则接通，媒体起不来主动拆；迟到的 200（本地已 CANCEL）必须 BYE 拆掉防幽灵通话。
**History-Info**（TS 24.604）：前转场景识别原始被叫；malformed 不杀呼叫。
**Gateway**：多设备注册表 + per-device worker（256 缓冲，满则丢弃）；事件去重；SimulateCall（测试用）。
**生产坑点**：15 项（100 占事务、BYE 先回、To tag、迟到 200 拆幽灵、本地腿 UDP、忙时清僵尸、History-Info 容错、呼叫等待特殊头等）。
**三级任务调度**：call.actor → agent.actor → goroutine，满都有日志，任务永不静默丢失。

### 2.4 dialog/媒体/客户端
**dialog/Controller**：不是 registry，是"注册派生头缓存"（20+ 字段：身份/网络/路由/IMS 头/语音特化/预构建头）。会话哈希做变更检测。CSeq 是 endpoint-wide（有意设计）。Contact 参数顺序可配（access_type/sip_instance/audio/smsip/icsi_ref），+sip.instance 用 IMEI。
**per-call dialog 状态**：learnVoiceDialog 从 200 OK 学 remoteTag/remoteTarget/Record-Route（反转）；advanceVoiceInviteCSeq 重置 RSeq（RFC 3262）。
**media/RTPRelay**：4 socket（RTP 双向 + RTCP 双向，RTCP=RTP+1）；原子指针 remoteAddr/clientAddr（LAN 从首包学习）；ptMap 双向原子替换；sendPaused（hold）；DSCP 46（EF）；100 次尝试绑连续端口。
**RTCP**：透传不解析；空 RR 保活（10s 无出站流量发假 RR，保 NAT 映射）；只有字节计数，无 jitter/loss。
**RTPMonitor**：双向超时（默认 10s→挂断）+ 单向超时（只告警不挂断，单通检测）；5s 检查间隔。
**DTMF**：三文件分工（发送入口/发送逻辑/流协调）；序列号连续性是核心（与语音包共享 RTP 流）；发送时丢弃冲突语音包；crypto/rand 初始化。
**ComfortNoise**：20ms PCMU，SSRC 0xdeadbeef，hold 时发声防死寂。
**PCAP**：标准格式 + 方向标记 + 音频录制配对。
**client/Bridge**：写池（4 worker+256 队列）、sipgo 事务、地址选择（跳过 rmnet/tun/tap/docker）、推送。PacketBridge 是手拼遗留，废弃。
**builder.go**：混合模式——INVITE 用 sipgo 对象，BYE/ACK/PRACK/UPDATE 手拼字符串（违反红线#1，蒸馏时改写）。业务知识保留：INVITE 头集合（Supported/Accept/Allow/Feature-Caps/P-Early-Media）、BYE/CANCEL Reason 头、phone-context 规则（TS 24.229）、SDP 内容（AMR-WB/AMR/telephone-event）、ACK 分支规则、PRACK RAck 头。
**生产坑点**：18 项（RTP 源校验防注入、LAN 地址学习做 NAT 穿透、DTMF 序列号连续、CANCEL/200 竞态、To tag 稳定性、紧急呼叫默认关闭等）。
**蒸馏策略**：ims-go dialog 的 registry 方向错误，改为 Controller 模式；builder 手拼部分改写为 sipgo 对象；依赖 imsendpoint/imscore/sipkit 需映射到 ims-go 抽象。

## 三、生产坑点汇总
（待填：从各分片注释中提取）

## 四、取舍方案（待用户评审）

### 取（必须蒸馏）
1. **callstate Actor**：有界队列（128）、非阻塞丢弃、panic 隔离——并发安全的基础
2. **Session Timer**（RFC 4028）：死呼叫检测最后防线，最高优先级；含 IR.92 特例
3. **PRACK Timer**：T1 退避 + generation 防 stale
4. **呼叫生命周期**：NoAnswer 定时器（60s）、幂等清理（cleanupOnce/doneOnce）、快照停止
5. **多呼叫**：max 2、呼叫等待（30s）、SwitchCall、自动提升
6. **Hold/Resume**（TS 24.610）：re-INVITE + SDP 方向重写 + relay 暂停
7. **SDP 全套**：offer 构造、precondition（RFC 3312）、codec 交集、重写（B2BUA 投影/SRTP 降级/telephone-event 补全）
8. **dialog Controller**：注册派生头缓存（替代 registry）+ per-call dialog 状态 + endpoint-wide CSeq
9. **media 全套**：4-socket relay、RTCP 空 RR 保活、RTPMonitor（双向/单向超时）、DTMF 状态机、DSCP 46、舒适噪声
10. **信令头知识**：INVITE 头集合、BYE/CANCEL Reason、phone-context、ACK 分支规则、PRACK RAck
11. **37 项生产坑点**：全部保留（见各分片报告）

### 舍（不蒸馏及理由）
1. **PacketBridge**：手拼字符串遗留，违反红线#1，用 client.Bridge（sipgo 对象）替代
2. **sdp_current.go**：历史兼容的第二套解析器，主流程不用
3. **PCAP/音频录制**：排障有用但非核心，放后续（可选）

### 取（修正）
**Gateway 多设备**：vohive-next 主项目用 `voiceGW.GetAgent(deviceID)` 按设备路由（5 处调用点），多设备是刚需。蒸馏时包含 Gateway（设备注册表 + per-device worker + 路由）。

### 改（架构适配点）
1. **imscore.Service → ims-go 接口**：EventBus/SetVoiceRequestHandler/Register/Unregister/RegState 映射到 internal/sip
2. **手拼 SIP → sipgo 对象**：builder.go 的 BYE/ACK/PRACK/UPDATE、dialog_signaling.go 的 string→parse 绕路（红线#1）
3. **SDP 策略**：offer 构造/改写用行处理（减少行为差异），解析用对象模型
4. **zap → slog**（D-008）；**包路径** → internal/voice/*（D-010）
5. **telephone-event fmtp**：0-15 vs 0-16 不一致，需确认后统一

### 待入站/出站分片报告后补充
已补完。Gateway 多设备模型：ims-go 单 Client 场景下暂不引入，如需多卡再加（待架构确认）。

## 五、蒸馏工作量估算

| 模块 | vowifi-go 行数 | 蒸馏要点 | 红线#1 改写量 |
|---|---|---|---|
| callstate | ~250 | 直接蒸馏（Actor/状态机） | 无 |
| 呼叫控制 | ~1500 | 生命周期/补充业务/定时器 | 中（buildIMS* 改写） |
| SDP | ~1200 | offer/协商/重写 | 低（SDP 行处理不违反红线） |
| dialog | ~600 | Controller 头缓存（替代 registry） | 无 |
| media | ~3000 | relay/monitor/rtcp/dtmf | 无 |
| 入站/出站 | ~2500 | B2BUA 双腿/方法分发 | 高（builder.go 手拼部分） |
| client | ~600 | Bridge（sipgo 路径） | 无（废弃 PacketBridge） |
| 总计 | ~9650 | | |

预计 ims-go voice 蒸馏后约 6000-7000 行（含测试）。
