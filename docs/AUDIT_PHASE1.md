# ims-go Phase 1 存量审计

> 方法：蒸馏四步法。审计目标：判定每个 WS 是"蒸馏/搬运可用"还是"简化/错模型需重做"。
> 标准：功能完整度对标 vowifi-go（生产验证），架构干净是 ims-go 的事。

## 三仓库子系统对照

| ims-go | vowifi-go | vowifi-core | WS | 行数对比 | 初判 |
|---|---|---|---|---|---|
| ims/ | runtimecore/ | runtimehost/ | WS-1 | - | 待审 |
| internal/sim | simauth/ | engine/sim, runtimehost/simauth | WS-2 | - | 待审 |
| internal/swu/* | engine/swu/* | engine/swu | WS-3 | 搬运 | 可用 |
| internal/netplane/* | ipsec3gpp, netstack, ipsec/driver | - | WS-4 | 搬运 | 可用 |
| internal/sip/register | imscore/register*.go (6412行) | - | WS-5 | 904行 vs 6412行 | **需深审** |
| internal/sip/transport | imscore/register_transport.go 等 | - | WS-6 | - | **需深审** |
| internal/sip/subscribe | imscore/subscribe*.go | - | WS-7 | - | **需深审** |
| internal/sip/sms | smsdelivery/ | runtimehost/messaging/ | WS-8 | 搬运+修复 | 可用 |
| internal/sip/ussd | ussi/ (INVITE/dialog) | runtimehost/messaging/ussd_xml.go | WS-9 | 模型错误 | **重做** |
| internal/sip/dialog,keepalive,inbound | imsdialog/, imscore | - | WS-10 | - | **需深审** |
| internal/voice | voice/ (128文件) | - | WS-11 | 简化 | **重做** |
| internal/voice/media | voice/media/ | - | WS-12 | 简化 | **重做** |
| internal/carrier,identity | profile/, epdg/ | runtimehost/carrier,identity | WS-13 | 新能力 | 待审 |
| internal/entitlement | entitlement/ | - | WS-14 | 薄 | 待审 |
| internal/xcap,emergency | emergency/ | runtimehost/e911 | WS-15 | 搬运/薄 | 待审 |
| observability | events/, logging/ | runtimehost/eventhost | WS-16 | 新 | 可用 |
| tests/e2e | - | - | WS-17 | 薄 | 可用 |
| carrier derive | - | - | WS-18 | 新 | 可用 |

## 深审记录（Phase1-3：算法/协议/新能力）

### WS-2 sim
**可用。** 1038行（digest/milenage/softsim/types），milenage从swu-go移植。vowifi-go simauth仅9行（别名），无对照压力。算法完整。

### WS-8 sms
**可用。** smscodec完整搬运→codec/，mo/mt自写，RP-DATA真实发送已修复（commit 36e6871）。

### WS-13 carrier/identity + WS-18 derive
**可用（新能力）。** 717行，P1推导引擎是新架构，无vowifi-go对照物。vowifi-go profile/（1031行）是SIM卡profile读取（TLV），属不同维度——后续可评估是否需要搬运SIM profile读取。

### WS-14 entitlement
**部分缺失。** ims-go 205行（TS.43主干），vowifi-go 928行（ts43 699行 + att provider 229行）。缺：ATT provider、EAP-AKA深度（eap_aka.go 187行）。TS.43主干可用，provider扩展需补。

### WS-15 xcap/emergency
**可用。** xcap三文件完整搬运（231/139/24行与vowifi-go一致）；emergency 100行 vs vowifi-go 82行，覆盖相当。

### WS-5 register
**判定：简化，不是蒸馏。** ims-go 约904行覆盖主干，vowifi-go 6412行的生产细节几乎全缺。
缺失（按严重程度）：
- A. 失败重试策略整套缺失：Retry-After不解析、423 Min-Expires不处理、305 Use-Proxy不处理、transport failure vs SIP拒绝不区分、无分级退避、AKA挑战无上限、AUTS无校验
- B. REGISTER请求头缺失：Feature-Caps、Contact +sip.instance/reg-id（无RFC 5626 outbound flow，互通硬伤）、P-Access-Network-Info、Supported: path/outbound、Route、Security-Client硬编码
- C. 200 OK后处理几乎没做：Expires不解析、P-Associated-URI丢弃、Service-Route/Path不捕获、多Contact不检测、无RFC 5626 flow协商
- D. Security policy只有494回显：无三模式（disabled/required/auto）、无offer选择、无SA复用
- E. Binding cleanup整套缺失：wildcard注销、重复绑定检测
- F. Refresh语义错误：重走完整Register而非复用session；刷新失败即判注册失败（无keepRegistrationAfterFailedRefresh）
- G. 传输层：强制TCP，无UDP候选，无DNS SRV
- H. Emergency REGISTER缺失（;sos、匿名模式）
注意：vowifi-go register是手拼SIP字符串，蒸馏时必须按红线#1改写为sipgo对象。

### WS-6 transport
**判定：简化，不是蒸馏，且存在零集成问题。**
集成状态：`Pipeline`/`Connect`/`OnConnectionLost` 从未被调用（死代码）；`OnConnectionLost` 无订阅者，"连接丢失触发恢复"只存在于注释里。
缺失：P-CSCF failover（按失败原因分类+惩罚期）、连接丢失→重注册链路、下行流证据追踪、双流概念（port-c/port-s分别管理）。

### WS-7 subscribe
**判定：简化，需蒸馏重做。** ims-go ~250行实现RFC 3265/6665 happy path，vowifi-go 1764行+的生产机制全缺。
P0协议正确性缺陷：CSeq死代码（递增但从未写入请求头，sipgo填随机CSeq）、只接受200不接受202、不解析Subscription-State、NOTIFY无dialog匹配（任意来源都接受）、无CSeq排序/去重。
P1生产健壮性全缺：481不重试、403/405/489无永久拒绝区分、无订阅前置门控、SUBSCRIBE失败无"不拆REGISTER"保护、无Timer N、不从响应学习Expires、无Retry-After、无route set/remote target学习、无并发飞行控制。
P2：MWI缺new/old计数、reginfo缺字段、无网络侧去注册检测、单Event无双订阅协调、缺IMS头、刷新用Expires/2而非(expires-advance)。
重做：骨架可保留，P0五项优先修，其余按蒸馏四步法搬入。

### WS-10 dialog/keepalive/inbound
**判定：简化，不是蒸馏，且存在零集成问题。**
集成状态：`dialog.Registry`、`keepalive.Keeper`、`inbound.Dispatcher` 全是死代码，无人 import/无人实例化（NOTIFY/MESSAGE 由 subscribe/sms 各自直连 server 注册）。
keepalive 缺失（vowifi-go 766行 vs ims-go 110行）：三级保活策略（CRLF优先/STUN/OPTIONS兜底）、RFC 5626 Flow-Timer、流量抑制、失败分类、STUN地址变化检测、CRLF pong超时追踪、OS级TCP keepalive、统一维护循环、OPTIONS请求缺IMS头（无Route会被严格P-CSCF拒绝）。
dialog 方向走偏：vowifi-go 根本没有 registry 概念，核心是"注册派生头缓存+完整IMS请求构建"；ims-go 做了 registry 却没做头缓存——本末倒置，构造出的 in-dialog 请求缺 Route/P-Preferred-Identity/Security-Verify，在真实 IMS 网络上不可用。另缺 dialog 状态机、入站 PRACK 处理。
inbound 缺失分发矩阵：INFO/BYE→USSI→voice→405、PRACK/UPDATE/REFER、ACK、默认405、DeliverResponse+CRLF pong信号。
返工优先级：keepalive → inbound → dialog（重做为头缓存+状态机）→ transport（failover+恢复链路打通）。

## 审计结论

### 分类清单

**直接可用（7）：** WS-2 sim、WS-3 swu、WS-4 netplane、WS-8 sms、WS-15 xcap/emergency、WS-16 observability、WS-17 e2e、WS-18 derive
（注：WS-13/18 为新能力，无对照物，按设计目标可用）

**部分缺失（1）：** WS-14 entitlement——缺 ATT provider、EAP-AKA 深度

**需蒸馏重做（7）：**
| WS | 包 | 核心问题 |
|---|---|---|
| WS-5 | sip/register | 简化：失败重试/B/C/D/E/F/G/H 全缺；vowifi-go 手拼 SIP，需按红线#1 改写为 sipgo 对象 |
| WS-6 | sip/transport | 简化 + 零集成 |
| WS-7 | sip/subscribe | 简化：P0 有 5 个协议正确性缺陷（CSeq 死代码、NOTIFY 无验证等） |
| WS-9 | sip/ussd | 模型错误：MESSAGE vs INVITE/dialog |
| WS-10 | sip/dialog,keepalive,inbound | 简化 + 零集成 |
| WS-11 | voice | 简化：85→4 文件，缺生命周期/定时器/补充业务/SDP |
| WS-12 | voice/media | 简化：缺 RTCP/监听/录制/舒适噪声等 |

### Phase 2 返工优先级

1. **WS-11 voice**（蒸馏笔记先行）——用户已定首个
2. **WS-5 register**——互通硬伤最多（RFC 5626、失败重试）
3. **WS-9 ussd**——模型错误，必须重写
4. **WS-7 subscribe**——P0 缺陷优先修
5. **WS-6/10 transport+dialog/keepalive/inbound**——先解决零集成，再补功能
6. **WS-12 media**——随 WS-11 蒸馏时一并处理
7. **WS-14 entitlement**——补 ATT provider

### 方法论备注
vowifi-go 的 register/ussd 等是手拼 SIP 字符串（违反红线#1），蒸馏时不能复制，必须重写为 sipgo 对象——这是"蒸馏"与"照搬"的本质区别，也是工作量所在。
