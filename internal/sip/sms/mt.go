package sms

import (
	"context"
	"sync"
	"time"

	"github.com/emiago/sipgo/sip"

	smscodec "github.com/voorz/ims-go/internal/sip/sms/codec"
)

// handleMessage 是 sipgo Server 的 MESSAGE 处理器（MT）。
func (s *SMS) handleMessage(req *sip.Request, tx sip.ServerTransaction) {
	// 立即回 200（先确认，再处理）
	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	_ = tx.Respond(res)

	// 解析发送方
	from := ""
	if h := req.GetHeader("From"); h != nil {
		from = h.Value()
	}
	body := req.Body()
	if len(body) == 0 {
		return
	}

	// 去重（基于 Call-ID + CSeq）
	dedupKey := ""
	if h := req.GetHeader("Call-ID"); h != nil {
		dedupKey = h.Value()
	}
	if h := req.GetHeader("CSeq"); h != nil {
		dedupKey += "|" + h.Value()
	}
	if dedupKey != "" {
		s.mu.Lock()
		if _, seen := s.seen[dedupKey]; seen {
			s.mu.Unlock()
			s.log.Debug("MT 短信去重", "key", dedupKey)
			return
		}
		s.seen[dedupKey] = time.Now()
		s.mu.Unlock()
	}

	// 尝试按 RP-DATA/TPDU 解码；失败则按纯文本处理
	text := string(body)
	if sender, decoded, _, concat, err := smscodec.DecodeDeliverTPDU(body); err == nil {
		if decoded != "" {
			text = decoded
		}
		if sender != "" {
			from = sender
		}
		// 分片重组
		if concat.Ref != 0 && concat.Total > 1 {
			if full, ok := s.reassemble(concat, []byte(text), from); ok {
				text = string(full)
			} else {
				return // 等更多分片
			}
		}
	}

	msg := Message{
		ID:   dedupKey,
		From: from,
		To:   s.cfg.IMPU,
		Text: text,
		At:   time.Now(),
	}
	if s.cfg.OnMessage != nil {
		s.cfg.OnMessage(msg)
	}
}

// reassemble 分片重组；集齐返回完整内容。
func (s *SMS) reassemble(concat smscodec.ConcatInfo, data []byte, from string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ra, ok := s.reassemb[uint16(concat.Ref)]
	if !ok {
		ra = &reassembly{
			total: uint8(concat.Total),
			parts: make(map[uint8][]byte),
			from:  from,
			at:    time.Now(),
		}
		s.reassemb[uint16(concat.Ref)] = ra
	}
	ra.parts[uint8(concat.Seq)] = data

	if uint8(len(ra.parts)) < ra.total {
		return nil, false
	}
	// 按序拼接
	var full []byte
	for i := uint8(1); i <= ra.total; i++ {
		full = append(full, ra.parts[i]...)
	}
	delete(s.reassemb, uint16(concat.Ref))
	return full, true
}

// memoryStore 是内存 DeliveryStore 实现。
type memoryStore struct {
	mu   sync.RWMutex
	recs map[string]DeliveryRecord
}

func newMemoryStore() *memoryStore {
	return &memoryStore{recs: make(map[string]DeliveryRecord)}
}

func (m *memoryStore) Save(ctx context.Context, rec DeliveryRecord) error {
	m.mu.Lock()
	m.recs[rec.MessageID] = rec
	m.mu.Unlock()
	return nil
}

func (m *memoryStore) Get(ctx context.Context, messageID string) (DeliveryRecord, error) {
	m.mu.RLock()
	rec, ok := m.recs[messageID]
	m.mu.RUnlock()
	if !ok {
		return DeliveryRecord{}, nil
	}
	return rec, nil
}

func (m *memoryStore) UpdateStatus(ctx context.Context, messageID string, status DeliveryStatus, errMsg string) error {
	m.mu.Lock()
	rec := m.recs[messageID]
	rec.Status = status
	rec.Error = errMsg
	rec.Attempts++
	m.recs[messageID] = rec
	m.mu.Unlock()
	return nil
}
