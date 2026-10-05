// Package media 提供语音媒体面（WS-12）。
//
// 覆盖：RTP 双向中继、PT 映射、DTMF（RFC 4733）、SDP 构造/解析/重写。
// SRTP/SDES 为可选子项，产品明确需要时再立项（本 WS 不做）。
package media

import (
	"log/slog"
	"net"
	"sync"
)

// Config 是媒体配置。
type Config struct {
	// LocalAddr 是本地 RTP 监听地址；空则自动选择。
	LocalAddr string
	// PTMap 是 payload type 映射（本地 PT → 远端 PT）。
	PTMap map[uint8]uint8
	// Logger 为空时用 slog 默认。
	Logger *slog.Logger
}

// RTPRelay 是 RTP 双向中继。
type RTPRelay struct {
	cfg Config
	log *slog.Logger

	mu        sync.Mutex
	conn      *net.UDPConn
	remote    *net.UDPAddr
	enabled   bool
	closed    chan struct{}
	closeOnce sync.Once
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
