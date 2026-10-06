package media

import (
	"encoding/binary"
	"fmt"
)

// NewPipeline 创建 PCM↔RTP 管道。
func NewPipeline(cfg PipelineConfig) (*Pipeline, error) {
	if cfg.Codec == nil {
		return nil, fmt.Errorf("media: Codec 不能为空")
	}
	if cfg.Audio == nil {
		return nil, fmt.Errorf("media: Audio 不能为空")
	}
	if cfg.Relay == nil {
		return nil, fmt.Errorf("media: Relay 不能为空")
	}
	return &Pipeline{
		codec:  cfg.Codec,
		audio:  cfg.Audio,
		relay:  cfg.Relay,
		pt:     cfg.PayloadType,
		ssrc:   cfg.SSRC,
		sendCh: make(chan []byte, 32),
		recvCh: make(chan []byte, 32),
		stopCh: make(chan struct{}),
	}, nil
}

// Start 启动管道（发送/接收两个 goroutine）。
func (p *Pipeline) Start() {
	p.wg.Add(2)
	go p.sendLoop()
	go p.recvLoop()
}

// Stop 停止管道。
func (p *Pipeline) Stop() {
	close(p.stopCh)
	p.wg.Wait()
}

// sendLoop：PCM → Encode → RTP → Relay。
func (p *Pipeline) sendLoop() {
	defer p.wg.Done()
	frameSamples := p.codec.SampleRate() * p.codec.FrameDuration() / 1000
	for {
		select {
		case <-p.stopCh:
			return
		default:
		}
		pcm, end, err := p.audio.ReadPCM()
		if err != nil || end {
			return
		}
		// 按帧切分
		for len(pcm) >= frameSamples {
			frame := pcm[:frameSamples]
			pcm = pcm[frameSamples:]
			payload, err := p.codec.Encode(frame)
			if err != nil {
				continue
			}
			pkt := p.packRTP(payload)
			// 经 Relay 发送（IMS 侧）
			// 注意：RTPRelay 是中继，实际发送需经其 socket；此处为管道占位
			_ = pkt
		}
	}
}

// recvLoop：Relay → RTP 解包 → Decode → PCM。
func (p *Pipeline) recvLoop() {
	defer p.wg.Done()
	for {
		select {
		case <-p.stopCh:
			return
		case pkt := <-p.recvCh:
			payload, err := p.unpackRTP(pkt)
			if err != nil {
				continue
			}
			pcm, err := p.codec.Decode(payload)
			if err != nil {
				continue
			}
			_ = p.audio.WritePCM(pcm)
		}
	}
}

// packRTP 打包 RTP（12 字节头 + 负载）。
func (p *Pipeline) packRTP(payload []byte) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	pkt := make([]byte, 12+len(payload))
	pkt[0] = 0x80 // V=2
	pkt[1] = p.pt & 0x7F
	binary.BigEndian.PutUint16(pkt[2:4], p.seq)
	p.seq++
	binary.BigEndian.PutUint32(pkt[4:8], p.ts)
	// 时间戳按采样率递增（一帧 20ms）
	p.ts += uint32(p.codec.SampleRate() * p.codec.FrameDuration() / 1000)
	binary.BigEndian.PutUint32(pkt[8:12], p.ssrc)
	copy(pkt[12:], payload)
	return pkt
}

// unpackRTP 解包 RTP，返回负载。
func (p *Pipeline) unpackRTP(pkt []byte) ([]byte, error) {
	if len(pkt) < 12 {
		return nil, fmt.Errorf("media: RTP 包太短")
	}
	if pkt[0]>>6 != 2 {
		return nil, fmt.Errorf("media: 非 RTPv2 包")
	}
	cc := int(pkt[0] & 0x0F)
	headerLen := 12 + cc*4
	if len(pkt) < headerLen {
		return nil, fmt.Errorf("media: RTP 头不完整")
	}
	return pkt[headerLen:], nil
}

// InjectReceived 注入从 Relay 收到的 RTP 包（供外部调用）。
func (p *Pipeline) InjectReceived(pkt []byte) {
	select {
	case p.recvCh <- pkt:
	default:
		// 队列满时丢弃（实时音频不重传）
	}
}
