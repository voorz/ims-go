# WS-12 蒸馏笔记：媒体（RTP/RTCP/DTMF）

## 现状

ims-go 有 496 行基础实现：RTP relay（双 socket）、RTP 头编解码、
DTMF RFC 4733 编解码、SDP 解析。vowifi-go 有 4412 行完整实现。

## 缺失的生产能力（必须补）

| 能力 | vowifi-go | 说明 |
|------|-----------|------|
| 4-socket relay | ✅ | IMS RTP/RTCP + LAN RTP/RTCP；当前只有 2 个 |
| RTPMonitor 单通监测 | ✅ | 检测单向音频（IMS→LAN 或 LAN→IMS 无包），生产排障必需 |
| RTCP 保活 | ✅ | 10s 间隔，防止 NAT 超时 |
| DTMF 连续序号 | ✅ | RFC 4733 要求事件内序号连续；当前只有编解码无发送器 |
| PT 双向映射 | ✅ | 当前只有单向 PTMap |

## 不取的

- `pcap.go`（344行）：抓包调试，ims-go 用 Wireshark keylog（D-013）已覆盖。
- `audio_capture.go`（394行）：本地音频采集是消费方职责（vohive-next 有硬件）。
- `comfort_noise.go`：舒适噪声生成，P2。
- vowifi-core 的 RTP QoS 统计：已在 WS-16 observability 设计，不重复。

## 创造点

1. **Monitor 回调标准化**：`OnOneWay(direction, duration)`，比 vowifi-go 的
   `any` 参数回调类型安全。
2. **RTCP 与 RTP 共享 Monitor**：单通判断综合 RTP+RTCP 活动，避免误报。

## 实现

- `rtcp.go`：RTCP socket 管理、SR/RR 解析（最小集）、keepalive 发送。
- `monitor.go`：RTPMonitor（atomic 计数 + LastActivity），单通检测。
- `dtmf_sender.go`：DTMFSender（连续序号、冗余发送）。
- `rtp.go`：扩展为 4-socket，接 Monitor。
