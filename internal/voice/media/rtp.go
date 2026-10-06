package media

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"time"
)

// NewRTPRelay 创建 RTP 中继（四 socket 模型）。
// cfg.LocalAddr：IMS 侧 RTP 监听地址
// cfg.LANAddr：LAN 侧 RTP 监听地址（空则用 LocalAddr+2）
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
	// IMS 侧 RTP
	imsAddr, err := net.ResolveUDPAddr("udp", cfg.LocalAddr)
	if err != nil {
		return nil, fmt.Errorf("media: 解析 IMS 地址失败: %w", err)
	}
	imsRTP, err := net.ListenUDP("udp", imsAddr)
	if err != nil {
		return nil, fmt.Errorf("media: IMS RTP 监听失败: %w", err)
	}
	// LAN 侧 RTP（默认 IMS 端口+2，避免冲突）
	lanAddrStr := cfg.LANAddr
	if lanAddrStr == "" {
		lanAddrStr = fmt.Sprintf("%s:%d",
			imsRTP.LocalAddr().(*net.UDPAddr).IP.String(),
			imsRTP.LocalAddr().(*net.UDPAddr).Port+2)
	}
	lanAddr, err := net.ResolveUDPAddr("udp", lanAddrStr)
	if err != nil {
		imsRTP.Close()
		return nil, fmt.Errorf("media: 解析 LAN 地址失败: %w", err)
	}
	lanRTP, err := net.ListenUDP("udp", lanAddr)
	if err != nil {
		imsRTP.Close()
		return nil, fmt.Errorf("media: LAN RTP 监听失败: %w", err)
	}

	r := &RTPRelay{
		cfg:    cfg,
		log:    cfg.Logger,
		imsRTP: imsRTP,
		lanRTP: lanRTP,
		closed: make(chan struct{}),
	}
	// RTCP sockets（RTP 端口+1，各侧独立）
	if cfg.EnableRTCP {
		// IMS RTCP
		imsRTCPAddr := &net.UDPAddr{
			IP:   imsRTP.LocalAddr().(*net.UDPAddr).IP,
			Port: imsRTP.LocalAddr().(*net.UDPAddr).Port + 1,
		}
		if c, err := net.ListenUDP("udp", imsRTCPAddr); err == nil {
			r.imsRTCP = c
		} else {
			r.log.Warn("IMS RTCP 监听失败", "error", err)
		}
		// LAN RTCP
		lanRTCPAddr := &net.UDPAddr{
			IP:   lanRTP.LocalAddr().(*net.UDPAddr).IP,
			Port: lanRTP.LocalAddr().(*net.UDPAddr).Port + 1,
		}
		if c, err := net.ListenUDP("udp", lanRTCPAddr); err == nil {
			r.lanRTCP = c
		} else {
			r.log.Warn("LAN RTCP 监听失败", "error", err)
		}
	}
	// 单通监测
	if cfg.MonitorTimeout > 0 {
		r.monitor = NewRTPMonitor()
		r.monitor.StartOneWayMonitor(cfg.MonitorTimeout, cfg.OnOneWay)
	}
	// 启动双向转发
	go r.imsToLANLoop()
	go r.lanToIMSLoop()
	if r.imsRTCP != nil {
		go r.rtcpLoop(r.imsRTCP, true)
	}
	if r.lanRTCP != nil {
		go r.rtcpLoop(r.lanRTCP, false)
	}
	return r, nil
}

// LocalAddr 返回 IMS 侧 RTP 监听地址。
func (r *RTPRelay) LocalAddr() *net.UDPAddr {
	return r.imsRTP.LocalAddr().(*net.UDPAddr)
}

// LANAddr 返回 LAN 侧 RTP 监听地址。
func (r *RTPRelay) LANAddr() *net.UDPAddr {
	return r.lanRTP.LocalAddr().(*net.UDPAddr)
}

// RTCPLocalAddr 返回 IMS 侧 RTCP 监听地址（未启用时 nil）。
func (r *RTPRelay) RTCPLocalAddr() *net.UDPAddr {
	if r.imsRTCP == nil {
		return nil
	}
	return r.imsRTCP.LocalAddr().(*net.UDPAddr)
}

// SetRemote 设置 IMS 远端地址并启用转发。
func (r *RTPRelay) SetRemote(addr *net.UDPAddr) {
	r.mu.Lock()
	r.imsAddr = addr
	r.enabled = true
	r.mu.Unlock()
	// loops 已在 NewRTPRelay 启动
}

// SetLANAddr 手动设置 LAN 目标地址（不学习时用）。
func (r *RTPRelay) SetLANAddr(addr *net.UDPAddr) {
	r.mu.Lock()
	r.lanAddr = addr
	r.mu.Unlock()
}

// Monitor 返回监测器（可为 nil）。
func (r *RTPRelay) Monitor() *RTPMonitor { return r.monitor }

// relayLoop 转发循环：收包 → PT 映射改写 → 发往远端。
// 方向：LAN→IMS（本地收到的包发往 IMS）。
// IMS→LAN 方向由对端 relay 实例处理（双实例模型）。
// imsToLANLoop：IMS→LAN 转发（从 imsRTP 读，发往 lanAddr）。
func (r *RTPRelay) imsToLANLoop() {
	buf := make([]byte, 2048)
	for {
		select {
		case <-r.closed:
			return
		default:
		}
		n, _, err := r.imsRTP.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-r.closed:
				return
			default:
				r.log.Warn("IMS RTP 读取失败", "error", err)
				continue
			}
		}
		if n < 12 {
			continue
		}
		r.mu.Lock()
		lanAddr := r.lanAddr
		enabled := r.enabled
		r.mu.Unlock()
		if !enabled || lanAddr == nil {
			continue
		}
		pkt := append([]byte(nil), buf[:n]...)
		// 反向 PT 映射
		if newPT, ok := r.cfg.ReversePTMap[pkt[1]&0x7F]; ok {
			pkt[1] = (pkt[1] & 0x80) | newPT
		}
		if r.monitor != nil {
			r.monitor.UpdateIMS()
		}
		atomic.AddUint64(&r.bytesIMSToLAN, uint64(len(pkt)))
		_, _ = r.lanRTP.WriteToUDP(pkt, lanAddr)
	}
}

// lanToIMSLoop：LAN→IMS 转发（从 lanRTP 读，发往 imsAddr）。
// LAN 地址学习：首包源地址即为 LAN 客户端。
func (r *RTPRelay) lanToIMSLoop() {
	buf := make([]byte, 2048)
	for {
		select {
		case <-r.closed:
			return
		default:
		}
		n, src, err := r.lanRTP.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-r.closed:
				return
			default:
				r.log.Warn("LAN RTP 读取失败", "error", err)
				continue
			}
		}
		if n < 12 {
			continue
		}
		r.mu.Lock()
		imsAddr := r.imsAddr
		enabled := r.enabled
		// 学习 LAN 地址（首包）
		if r.lanAddr == nil {
			r.lanAddr = src
			r.log.Debug("学习到 LAN 客户端地址", "addr", src)
		}
		r.mu.Unlock()
		if !enabled || imsAddr == nil {
			continue
		}
		pkt := append([]byte(nil), buf[:n]...)
		// 正向 PT 映射
		if newPT, ok := r.cfg.PTMap[pkt[1]&0x7F]; ok {
			pkt[1] = (pkt[1] & 0x80) | newPT
		}
		if r.monitor != nil {
			r.monitor.UpdateLAN()
		}
		atomic.AddUint64(&r.bytesLANToIMS, uint64(len(pkt)))
		_, _ = r.imsRTP.WriteToUDP(pkt, imsAddr)
	}
}

// Stats 返回双向字节计数。
func (r *RTPRelay) Stats() (imsToLAN, lanToIMS uint64) {
	return atomic.LoadUint64(&r.bytesIMSToLAN), atomic.LoadUint64(&r.bytesLANToIMS)
}

// rtcpLoop RTCP 转发循环（四 socket：按方向透传）。
// isIMS 为 true 时是 IMS 侧 RTCP（收 IMS → 发 LAN），否则反之。
func (r *RTPRelay) rtcpLoop(conn *net.UDPConn, isIMS bool) {
	buf := make([]byte, 2048)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.closed:
			return
		case <-ticker.C:
			r.sendRTCPKeealive(conn, isIMS)
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		if n < 4 {
			continue
		}
		// RTCP 透传到对端
		r.mu.Lock()
		var target *net.UDPAddr
		var targetConn *net.UDPConn
		if isIMS {
			target = r.lanAddr
			if target != nil {
				target = &net.UDPAddr{IP: target.IP, Port: target.Port + 1}
			}
			targetConn = r.lanRTCP
		} else {
			target = r.imsAddr
			if target != nil {
				target = &net.UDPAddr{IP: target.IP, Port: target.Port + 1}
			}
			targetConn = r.imsRTCP
		}
		enabled := r.enabled
		r.mu.Unlock()
		if enabled && target != nil && targetConn != nil {
			_, _ = targetConn.WriteToUDP(buf[:n], target)
		}
	}
}

// sendRTCPKeealive 发送 RTCP 保活（空 Receiver Report）。
func (r *RTPRelay) sendRTCPKeealive(conn *net.UDPConn, isIMS bool) {
	r.mu.Lock()
	var target *net.UDPAddr
	if isIMS {
		target = r.lanAddr
	} else {
		target = r.imsAddr
	}
	enabled := r.enabled
	r.mu.Unlock()
	if !enabled || target == nil {
		return
	}
	target = &net.UDPAddr{IP: target.IP, Port: target.Port + 1}
	// 最小 RR：V=2, PT=201, length=1, SSRC=0
	pkt := []byte{0x80, 0xC9, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00}
	_, _ = conn.WriteToUDP(pkt, target)
}

// Close 关闭中继（四 socket 全关）。
func (r *RTPRelay) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	if r.monitor != nil {
		r.monitor.Stop()
	}
	if r.imsRTCP != nil {
		_ = r.imsRTCP.Close()
	}
	if r.lanRTCP != nil {
		_ = r.lanRTCP.Close()
	}
	_ = r.lanRTP.Close()
	return r.imsRTP.Close()
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
