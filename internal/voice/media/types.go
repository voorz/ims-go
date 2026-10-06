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
	// LocalAddr 是本地 RTP 监听地址；空则自动选择。
	LocalAddr string
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

	mu         sync.Mutex
	conn       *net.UDPConn // LAN RTP
	rtcpConn   *net.UDPConn // LAN RTCP（可选）
	remote     *net.UDPAddr // IMS RTP 远端
	remoteRTCP *net.UDPAddr // IMS RTCP 远端
	enabled    bool
	closed     chan struct{}
	closeOnce  sync.Once

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
