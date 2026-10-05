package media

import (
	"encoding/binary"
	"fmt"
)

// EncodeDTMF 编码 RFC 4733 DTMF 事件为 RTP payload。
func EncodeDTMF(e DTMFEvent) []byte {
	b := make([]byte, 4)
	b[0] = e.Event
	if e.End {
		b[1] = 0x80 | (e.Volume & 0x3F)
	} else {
		b[1] = e.Volume & 0x3F
	}
	binary.BigEndian.PutUint16(b[2:4], e.Duration)
	return b
}

// DecodeDTMF 解码 RFC 4733 DTMF 事件。
func DecodeDTMF(b []byte) (DTMFEvent, error) {
	if len(b) < 4 {
		return DTMFEvent{}, fmt.Errorf("media: DTMF payload 太短")
	}
	return DTMFEvent{
		Event:    b[0],
		End:      b[1]&0x80 != 0,
		Volume:   b[1] & 0x3F,
		Duration: binary.BigEndian.Uint16(b[2:4]),
	}, nil
}

// DTMFEventForChar 将字符映射为 DTMF 事件号。
func DTMFEventForChar(c byte) (uint8, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c == '*':
		return 10, true
	case c == '#':
		return 11, true
	case c >= 'A' && c <= 'D':
		return 12 + (c - 'A'), true
	case c >= 'a' && c <= 'd':
		return 12 + (c - 'a'), true
	}
	return 0, false
}
