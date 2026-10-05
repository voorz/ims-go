package media

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
)

// NewRTPRelay 创建 RTP 中继（监听本地端口）。
func NewRTPRelay(cfg Config) (*RTPRelay, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	addr, err := net.ResolveUDPAddr("udp", cfg.LocalAddr)
	if err != nil {
		return nil, fmt.Errorf("media: 解析地址失败: %w", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("media: 监听失败: %w", err)
	}
	return &RTPRelay{
		cfg:    cfg,
		log:    cfg.Logger,
		conn:   conn,
		closed: make(chan struct{}),
	}, nil
}

// LocalAddr 返回本地监听地址。
func (r *RTPRelay) LocalAddr() *net.UDPAddr {
	return r.conn.LocalAddr().(*net.UDPAddr)
}

// SetRemote 设置远端地址并启用转发。
func (r *RTPRelay) SetRemote(addr *net.UDPAddr) {
	r.mu.Lock()
	r.remote = addr
	r.enabled = true
	r.mu.Unlock()
	go r.relayLoop()
}

// relayLoop 转发循环：收包 → PT 映射改写 → 发往远端。
func (r *RTPRelay) relayLoop() {
	buf := make([]byte, 2048)
	for {
		select {
		case <-r.closed:
			return
		default:
		}
		n, _, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-r.closed:
				return
			default:
				r.log.Warn("RTP 读取失败", "error", err)
				continue
			}
		}
		if n < 12 {
			continue
		}
		// PT 映射改写
		pkt := append([]byte(nil), buf[:n]...)
		if newPT, ok := r.cfg.PTMap[pkt[1]&0x7F]; ok {
			pkt[1] = (pkt[1] & 0x80) | newPT
		}
		r.mu.Lock()
		remote := r.remote
		r.mu.Unlock()
		if remote != nil {
			_, _ = r.conn.WriteToUDP(pkt, remote)
		}
	}
}

// Close 关闭中继。
func (r *RTPRelay) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return r.conn.Close()
}

// ParseRTPHeader 解析 RTP 头。
func ParseRTPHeader(b []byte) (RTPHeader, error) {
	if len(b) < 12 {
		return RTPHeader{}, fmt.Errorf("media: RTP 包太短")
	}
	return RTPHeader{
		Version:     b[0] >> 6,
		Padding:     b[0]&0x20 != 0,
		Extension:   b[0]&0x10 != 0,
		CSRCCount:   b[0] & 0x0F,
		Marker:      b[1]&0x80 != 0,
		PayloadType: b[1] & 0x7F,
		Seq:         binary.BigEndian.Uint16(b[2:4]),
		Timestamp:   binary.BigEndian.Uint32(b[4:8]),
		SSRC:        binary.BigEndian.Uint32(b[8:12]),
	}, nil
}

// MarshalRTPHeader 序列化 RTP 头。
func MarshalRTPHeader(h RTPHeader) []byte {
	b := make([]byte, 12)
	b[0] = (h.Version << 6)
	if h.Padding {
		b[0] |= 0x20
	}
	if h.Extension {
		b[0] |= 0x10
	}
	b[0] |= h.CSRCCount & 0x0F
	b[1] = h.PayloadType & 0x7F
	if h.Marker {
		b[1] |= 0x80
	}
	binary.BigEndian.PutUint16(b[2:4], h.Seq)
	binary.BigEndian.PutUint32(b[4:8], h.Timestamp)
	binary.BigEndian.PutUint32(b[8:12], h.SSRC)
	return b
}
