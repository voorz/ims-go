// Package media 提供语音媒体面（WS-12 蒸馏重做）。
//
// 覆盖：RTP/RTCP 4-socket 双向中继、PT 双向映射、DTMF（RFC 4733 发送器）、
// SDP 构造/解析/重写、单通监测、RTCP 保活。
// SRTP/SDES 为可选子项，产品明确需要时再立项（本 WS 不做）。
package media

import (
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Config 是媒体配置。
type Config struct {
	// LocalAddr 是 IMS 侧 RTP 监听地址；空则自动选择。
	LocalAddr string
	// LANAddr 是 LAN 侧 RTP 监听地址；空则用 LocalAddr 端口+2。
	LANAddr string
	// PTMap 是 payload type 映射（本地 PT → 远端 PT）。
	PTMap map[uint8]uint8
	// ReversePTMap 是反向映射（远端 PT → 本地 PT）；空则自动反转 PTMap。
	ReversePTMap map[uint8]uint8
	// EnableRTCP 为 true 时启用 RTCP socket（4-socket 模式）。
	EnableRTCP bool
	// MonitorTimeout 是单通监测超时；0 用默认 10s。<=0 禁用监测。
	MonitorTimeout time.Duration
	// OnOneWay 是单通回调（direction: "IMS->LAN" 或 "LAN->IMS"）。
	OnOneWay func(direction string, silentFor time.Duration)
	// Logger 为空时用 slog 默认。
	Logger *slog.Logger
}

// RTPRelay 是 RTP/RTCP 双向中继。
type RTPRelay struct {
	cfg Config
	log *slog.Logger

	mu sync.Mutex
	// 四 socket 模型（vowifi-go 生产架构）：
	//   imsRTP/imxRTCP：IMS 侧（收 IMS，发往 LAN）
	//   lanRTP/lanRTCP：LAN 侧（收 LAN，发往 IMS）
	// 职责分离：无地址学习歧义，独立生命周期，NAT 友好。
	imsRTP  *net.UDPConn
	lanRTP  *net.UDPConn
	imsRTCP *net.UDPConn // 可选
	lanRTCP *net.UDPConn // 可选

	imsAddr *net.UDPAddr // IMS RTP 远端（发往 IMS 的目标）
	lanAddr *net.UDPAddr // LAN RTP 远端（发往 LAN 的目标，学习或配置）

	enabled   bool
	closed    chan struct{}
	closeOnce sync.Once

	// 双向字节计数（vowifi-go 生产模型）
	bytesIMSToLAN uint64
	bytesLANToIMS uint64

	monitor *RTPMonitor
}

// RTPMonitor 监测 RTP 双向活动（单通检测）。
type RTPMonitor struct {
	lastIMStoLAN atomic.Int64 // UnixNano
	lastLANtoIMS atomic.Int64
	imsCount     atomic.Uint64
	lanCount     atomic.Uint64
	stopCh       chan struct{}
}

// RTPHeader 是 12 字节 RTP 头。
type RTPHeader struct {
	Version     uint8
	Padding     bool
	Extension   bool
	CSRCCount   uint8
	Marker      bool
	PayloadType uint8
	Seq         uint16
	Timestamp   uint32
	SSRC        uint32
}

// DTMFEvent 是 RFC 4733 DTMF 事件。
type DTMFEvent struct {
	Event    uint8 // 0-9, *, #, A-D
	End      bool  // 结束位
	Volume   uint8
	Duration uint16
}

// SDP 是简化的 SDP 对象模型。
type SDP struct {
	Connection string // c= 行的地址
	Media      []SDPMedia
}

// SDPMedia 是一路媒体。
type SDPMedia struct {
	Type    string // "audio"
	Port    int
	Proto   string   // "RTP/AVP"
	Formats []string // PT 列表
	Attrs   map[string]string
}

// DTMFSender 发送 RFC 4733 DTMF（连续序号、冗余）。
type DTMFSender struct {
	seq       atomic.Uint32 // RTP 序号（连续）
	timestamp atomic.Uint32
	ssrc      uint32
	pt        uint8 // DTMF payload type

	send func(pkt []byte) error
}

// PipelineConfig 是 PCM↔RTP 管道配置（A2-6）。
type PipelineConfig struct {
	Codec       Codec
	Audio       AudioIO
	Relay       *RTPRelay
	PayloadType uint8
	SSRC        uint32
}

// AudioIO 是 PCM 音频接口（与 voice.AudioIO 同构，避免循环导入）。
type AudioIO interface {
	ReadPCM() (pcm []int16, end bool, err error)
	WritePCM(pcm []int16) error
	SampleRate() int
	Close() error
}

// Codec 是音频编解码器接口（A2-6）。
//
// 设计说明：AMR/AMR-WB 的 DSP 核心（3GPP TS 26.071）是数周工作量，
// 不在本阶段实现。本接口可插拔：生产环境注入真实 DSP 实现
// （如 opencore-amr 的 cgo 封装，或纯 Go 实现），测试用 NullCodec。
//
// RTP 打包（RFC 4867）由 Pipeline 负责，与 DSP 解耦。
type Codec interface {
	// Name 返回编解码器名（"AMR" / "AMR-WB"）。
	Name() string
	// SampleRate 返回采样率（AMR=8000，AMR-WB=16000）。
	SampleRate() int
	// FrameDuration 返回每帧时长（毫秒，AMR=20）。
	FrameDuration() int
	// Encode 将 PCM 编码为负载字节；pcm 长度应为一帧采样数。
	Encode(pcm []int16) ([]byte, error)
	// Decode 将负载字节解码为 PCM。
	Decode(data []byte) ([]int16, error)
}

// NullCodec 是直通编解码器（测试用）。
// Encode 将 int16 PCM 转为字节（小端），Decode 反之。无压缩，仅用于管道联调。
type NullCodec struct {
	name       string
	sampleRate int
}

// Pipeline 是 PCM↔RTP 管道（A2-6）。
//
// 数据流：
//
//	发送：AudioIO.ReadPCM() → Codec.Encode() → RTP 打包 → Relay 发送
//	接收：Relay 接收 → RTP 解包 → Codec.Decode() → AudioIO.WritePCM()
//
// RTP 头（12 字节，RFC 3550）：
//
//	 0                   1                   2                   3
//	 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|V=2|P|X|  CC   |M|     PT      |       sequence number         |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|                           timestamp                           |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|           synchronization source (SSRC) identifier            |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
type Pipeline struct {
	codec  Codec
	audio  AudioIO
	relay  *RTPRelay
	pt     uint8 // payload type（AMR=114 动态，AMR-WB=115 动态，实际由 SDP 协商）
	ssrc   uint32
	seq    uint16
	ts     uint32
	mu     sync.Mutex
	sendCh chan []byte
	recvCh chan []byte
	stopCh chan struct{}
	wg     sync.WaitGroup
}
