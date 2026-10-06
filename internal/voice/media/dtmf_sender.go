package media

import (
	"encoding/binary"
	"sync/atomic"
	"time"
)

// DTMFSender 发送 RFC 4733 DTMF（连续序号、冗余）。
type DTMFSender struct {
	seq       atomic.Uint32 // RTP 序号（连续）
	timestamp atomic.Uint32
	ssrc      uint32
	pt        uint8 // DTMF payload type

	send func(pkt []byte) error
}

// NewDTMFSender 创建 DTMF 发送器。
// send 是底层 RTP 发送函数（已组好头的包直接发送）。
func NewDTMFSender(ssrc uint32, dtmfPT uint8, send func(pkt []byte) error) *DTMFSender {
	return &DTMFSender{ssrc: ssrc, pt: dtmfPT, send: send}
}

// Send 发送一个 DTMF 字符（RFC 4733：开始包 + 冗余 + 结束包）。
// duration 是每个包的时长增量（典型 160 = 20ms @ 8kHz）。
func (d *DTMFSender) Send(event uint8, duration uint16) error {
	if d.send == nil {
		return nil
	}
	// 开始包（3 次冗余，序号连续，时间戳相同）
	ts := d.timestamp.Add(uint32(duration))
	for i := 0; i < 3; i++ {
		pkt := d.buildPacket(event, false, 0, duration, ts)
		if err := d.send(pkt); err != nil {
			return err
		}
	}
	// 结束包（3 次，End 位，时长累加）
	var endDuration uint16 = duration
	for i := 0; i < 3; i++ {
		endDuration += duration
		pkt := d.buildPacket(event, true, 0, endDuration, ts)
		if err := d.send(pkt); err != nil {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

// buildPacket 构造 RTP DTMF 包（12 字节头 + 4 字节事件）。
func (d *DTMFSender) buildPacket(event uint8, end bool, volume uint8, duration uint16, timestamp uint32) []byte {
	pkt := make([]byte, 16)
	pkt[0] = 0x80 // V=2
	pkt[1] = d.pt & 0x7F
	seq := uint16(d.seq.Add(1))
	binary.BigEndian.PutUint16(pkt[2:4], seq)
	binary.BigEndian.PutUint32(pkt[4:8], timestamp)
	binary.BigEndian.PutUint32(pkt[8:12], d.ssrc)
	// DTMF 事件
	pkt[12] = event
	if end {
		pkt[13] = 0x80 | (volume & 0x3F)
	} else {
		pkt[13] = volume & 0x3F
	}
	binary.BigEndian.PutUint16(pkt[14:16], duration)
	return pkt
}
