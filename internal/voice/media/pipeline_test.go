package media

import (
	"testing"
)

// TestPipelineRTPCodec 测试 Pipeline 的 RTP 打包/解包 + 编解码往返。
// 不依赖真实 UDP（用 NullCodec + 内存通道）。
func TestPipelineRTPCodec(t *testing.T) {
	codec := NewNullCodec("AMR", 8000)
	// 构造最小 Pipeline（不启动 relay，仅测 pack/unpack）
	p := &Pipeline{
		codec:  codec,
		pt:     114,
		ssrc:   0x12345678,
		sendCh: make(chan []byte, 32),
		recvCh: make(chan []byte, 32),
		stopCh: make(chan struct{}),
	}

	// 160 采样 = 20ms @ 8kHz
	pcm := make([]int16, 160)
	for i := range pcm {
		pcm[i] = int16(i * 100)
	}
	payload, err := codec.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	pkt := p.packRTP(payload)
	if len(pkt) < 12+len(payload) {
		t.Fatalf("RTP 包长度不对: %d", len(pkt))
	}
	// 验证 RTP 头
	if pkt[0] != 0x80 {
		t.Errorf("Version = %d，期望 2", pkt[0]>>6)
	}
	if pkt[1]&0x7F != 114 {
		t.Errorf("PT = %d，期望 114", pkt[1]&0x7F)
	}
	// 解包
	gotPayload, err := p.unpackRTP(pkt)
	if err != nil {
		t.Fatalf("unpackRTP: %v", err)
	}
	gotPCM, err := codec.Decode(gotPayload)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(gotPCM) != len(pcm) {
		t.Fatalf("PCM 长度 %d，期望 %d", len(gotPCM), len(pcm))
	}
	for i := range pcm {
		if gotPCM[i] != pcm[i] {
			t.Fatalf("采样 %d 不匹配: %d != %d", i, gotPCM[i], pcm[i])
		}
	}
}

// TestPipelineSeqIncrement 测试序号递增。
func TestPipelineSeqIncrement(t *testing.T) {
	codec := NewNullCodec("AMR-WB", 16000)
	p := &Pipeline{codec: codec, pt: 115, ssrc: 1, stopCh: make(chan struct{})}
	pkt1 := p.packRTP([]byte{1, 2, 3})
	pkt2 := p.packRTP([]byte{4, 5, 6})
	// 序号在字节 2-3
	seq1 := int(pkt1[2])<<8 | int(pkt1[3])
	seq2 := int(pkt2[2])<<8 | int(pkt2[3])
	if seq2 != seq1+1 {
		t.Errorf("序号未递增: %d -> %d", seq1, seq2)
	}
	// 时间戳递增（一帧 20ms @ 16kHz = 320 采样）
	ts1 := int(pkt1[4])<<24 | int(pkt1[5])<<16 | int(pkt1[6])<<8 | int(pkt1[7])
	ts2 := int(pkt2[4])<<24 | int(pkt2[5])<<16 | int(pkt2[6])<<8 | int(pkt2[7])
	if ts2-ts1 != 320 {
		t.Errorf("时间戳增量 %d，期望 320", ts2-ts1)
	}
}
