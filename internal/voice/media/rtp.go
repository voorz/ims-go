package media

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"time"
)

// NewRTPRelay 创建 RTP 中继（监听本地端口）。
func NewRTPRelay(cfg Config) (*RTPRelay, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	// 补全反向 PT 映射
	if len(cfg.ReversePTMap) == 0 && len(cfg.PTMap) > 0 {
		cfg.ReversePTMap = make(map[uint8]uint8, len(cfg.PTMap))
		for k, v := range cfg.PTMap {
			cfg.ReversePTMap[v] = k
		}
	}
	addr, err := net.ResolveUDPAddr("udp", cfg.LocalAddr)
	if err != nil {
		return nil, fmt.Errorf("media: 解析地址失败: %w", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("media: 监听失败: %w", err)
	}
	r := &RTPRelay{
		cfg:    cfg,
		log:    cfg.Logger,
		conn:   conn,
		closed: make(chan struct{}),
	}
	// RTCP socket（RTP 端口 +1）
	if cfg.EnableRTCP {
		rtcpAddr := &net.UDPAddr{
			IP:   conn.LocalAddr().(*net.UDPAddr).IP,
			Port: conn.LocalAddr().(*net.UDPAddr).Port + 1,
		}
		if rtcpConn, err := net.ListenUDP("udp", rtcpAddr); err == nil {
			r.rtcpConn = rtcpConn
		} else {
			r.log.Warn("RTCP 监听失败（继续无 RTCP 模式）", "error", err)
		}
	}
	// 单通监测
	if cfg.MonitorTimeout > 0 {
		r.monitor = NewRTPMonitor()
		r.monitor.StartOneWayMonitor(cfg.MonitorTimeout, cfg.OnOneWay)
	}
	return r, nil
}

// LocalAddr 返回本地 RTP 监听地址。
func (r *RTPRelay) LocalAddr() *net.UDPAddr {
	return r.conn.LocalAddr().(*net.UDPAddr)
}

// RTCPLocalAddr 返回本地 RTCP 监听地址（未启用时 nil）。
func (r *RTPRelay) RTCPLocalAddr() *net.UDPAddr {
	if r.rtcpConn == nil {
		return nil
	}
	return r.rtcpConn.LocalAddr().(*net.UDPAddr)
}

// SetRemote 设置 IMS 远端地址并启用转发。
func (r *RTPRelay) SetRemote(addr *net.UDPAddr) {
	r.mu.Lock()
	r.remote = addr
	// RTCP 远端默认为 RTP 远端端口 +1
	if addr != nil {
		r.remoteRTCP = &net.UDPAddr{IP: addr.IP, Port: addr.Port + 1}
	}
	r.enabled = true
	r.mu.Unlock()
	go r.relayLoop()
	if r.rtcpConn != nil {
		go r.rtcpLoop()
	}
}

// Monitor 返回监测器（可为 nil）。
func (r *RTPRelay) Monitor() *RTPMonitor { return r.monitor }

// relayLoop 转发循环：收包 → PT 映射改写 → 发往远端。
// 方向：LAN→IMS（本地收到的包发往 IMS）。
// IMS→LAN 方向由对端 relay 实例处理（双实例模型）。
func (r *RTPRelay) relayLoop() {
	buf := make([]byte, 2048)
	for {
		select {
		case <-r.closed:
			return
		default:
		}
		n, src, err := r.conn.ReadFromUDP(buf)
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
		r.mu.Lock()
		remote := r.remote
		lanAddr := r.lanAddr
		enabled := r.enabled
		r.mu.Unlock()
		if !enabled {
			continue
		}

		pkt := append([]byte(nil), buf[:n]...)
		isFromIMS := remote != nil && src.IP.Equal(remote.IP) && src.Port == remote.Port
		if isFromIMS {
			// IMS→LAN：反向 PT 映射，转发给学习到的 LAN 客户端
			if newPT, ok := r.cfg.ReversePTMap[pkt[1]&0x7F]; ok {
				pkt[1] = (pkt[1] & 0x80) | newPT
			}
			if r.monitor != nil {
				r.monitor.UpdateIMS()
			}
			atomic.AddUint64(&r.bytesIMSToLAN, uint64(len(pkt)))
			if lanAddr != nil {
				_, _ = r.conn.WriteToUDP(pkt, lanAddr)
			} else {
				r.log.Debug("IMS→LAN：LAN 地址未学习，丢弃", "src", src)
			}
			continue
		}
		// LAN→IMS：学习 LAN 客户端地址，正向 PT 映射
		r.mu.Lock()
		if r.lanAddr == nil {
			r.lanAddr = src
			r.lanAddrRTCP = &net.UDPAddr{IP: src.IP, Port: src.Port + 1}
			r.log.Debug("学习到 LAN 客户端地址", "addr", src)
		}
		r.mu.Unlock()
		if newPT, ok := r.cfg.PTMap[pkt[1]&0x7F]; ok {
			pkt[1] = (pkt[1] & 0x80) | newPT
		}
		if r.monitor != nil {
			r.monitor.UpdateLAN()
		}
		atomic.AddUint64(&r.bytesLANToIMS, uint64(len(pkt)))
		if remote != nil {
			_, _ = r.conn.WriteToUDP(pkt, remote)
		}
	}
}

// Stats 返回双向字节计数。
func (r *RTPRelay) Stats() (imsToLAN, lanToIMS uint64) {
	return atomic.LoadUint64(&r.bytesIMSToLAN), atomic.LoadUint64(&r.bytesLANToIMS)
}

// rtcpLoop RTCP 转发循环（最小实现：透传 + 保活）。
func (r *RTPRelay) rtcpLoop() {
	buf := make([]byte, 2048)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.closed:
			return
		case <-ticker.C:
			// RTCP 保活：定期发送空 RR（防止 NAT 超时）。
			r.sendRTCPKeealive()
		default:
		}
		_ = r.rtcpConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, _, err := r.rtcpConn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		if n < 4 {
			continue
		}
		// RTCP 透传到对端
		r.mu.Lock()
		remoteRTCP := r.remoteRTCP
		r.mu.Unlock()
		if remoteRTCP != nil {
			_, _ = r.rtcpConn.WriteToUDP(buf[:n], remoteRTCP)
		}
	}
}

// sendRTCPKeealive 发送 RTCP 保活（空 Receiver Report）。
func (r *RTPRelay) sendRTCPKeealive() {
	r.mu.Lock()
	remoteRTCP := r.remoteRTCP
	enabled := r.enabled
	r.mu.Unlock()
	if !enabled || remoteRTCP == nil || r.rtcpConn == nil {
		return
	}
	// 最小 RR：V=2, PT=201, length=1, SSRC=0
	pkt := []byte{0x80, 0xC9, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00}
	_, _ = r.rtcpConn.WriteToUDP(pkt, remoteRTCP)
}

// Close 关闭中继。
func (r *RTPRelay) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	if r.monitor != nil {
		r.monitor.Stop()
	}
	if r.rtcpConn != nil {
		_ = r.rtcpConn.Close()
	}
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
