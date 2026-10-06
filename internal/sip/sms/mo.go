package sms

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/warthog618/sms/encoding/tpdu"

	smscodec "github.com/voorz/ims-go/internal/sip/sms/codec"
	"github.com/voorz/ims-go/internal/sip/transport"
)

// New 创建 SMS 模块并注册 MESSAGE 处理器。
func New(cfg Config) *SMS {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	store := cfg.Store
	if store == nil {
		store = newMemoryStore()
	}
	s := &SMS{
		cfg:      cfg,
		log:      cfg.Logger,
		store:    store,
		seen:     make(map[string]time.Time),
		reassemb: make(map[uint16]*reassembly),
	}
	cfg.Server.OnRequest(sip.MESSAGE, s.handleMessage)
	return s
}

// Send 发送 MO 短信（分片、队列、重试、投递状态机）。
func (s *SMS) Send(ctx context.Context, to, text string) (string, error) {
	return s.SendWithEncoding(ctx, to, text, "")
}

// SendWithEncoding 发送短信，可指定编码（"ucs2" 强制 UCS2，空/auto 自动选择）。
func (s *SMS) SendWithEncoding(ctx context.Context, to, text, encoding string) (string, error) {
	id := fmt.Sprintf("sms-%d", time.Now().UnixNano())
	msg := Message{
		ID:       id,
		From:     s.cfg.IMPU,
		To:       to,
		Text:     text,
		Encoding: encoding,
		At:       time.Now(),
	}

	// 编码（自动分片）
	opts := smscodec.SubmitOptions{}
	if enc, err := smscodec.NormalizeSMSEncoding(encoding); err == nil {
		opts.Encoding = enc
	}
	tpdus, err := smscodec.BuildSubmitTPDUObjectsWithOptions(to, text, opts)
	if err != nil {
		return "", fmt.Errorf("sms: PDU 编码失败: %w", err)
	}

	rec := DeliveryRecord{
		MessageID: id,
		To:        to,
		Status:    StatusQueued,
		At:        time.Now(),
	}
	if err := s.store.Save(ctx, rec); err != nil {
		return "", err
	}

	// 发送每片
	for i, tpdu := range tpdus {
		if err := s.sendSegment(ctx, msg, tpdu, i, len(tpdus)); err != nil {
			_ = s.store.UpdateStatus(ctx, id, StatusFailed, err.Error())
			return id, fmt.Errorf("sms: 分片 %d 发送失败: %w", i, err)
		}
	}

	_ = s.store.UpdateStatus(ctx, id, StatusSent, "")
	return id, nil
}

// sendSegment 发送单个分片（带重试）。
// TPDU 经 MarshalBinary 编码 → RP-DATA 封装 → SIP MESSAGE（application/vnd.3gpp.sms）。
func (s *SMS) sendSegment(ctx context.Context, msg Message, tpdu tpdu.TPDU, idx, total int) error {
	tpduBytes, err := tpdu.MarshalBinary()
	if err != nil {
		return fmt.Errorf("sms: TPDU 编码失败: %w", err)
	}
	// RP-MR 用分片序号；SMSC 为空（由网络填充）
	rpData := smscodec.BuildRPData(byte(idx), tpduBytes, "")

	var lastErr error
	for attempt := 0; attempt <= s.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			s.log.Info("SMS 重试", "id", msg.ID, "attempt", attempt)
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		if err := s.sendRPData(ctx, msg.To, rpData); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// sendRPData 经 SIP MESSAGE 发送 RP-DATA。
func (s *SMS) sendRPData(ctx context.Context, to string, rpData []byte) error {
	recipient := sip.Uri{Host: to}
	recipient.UriParams = sip.HeaderParams{{K: "transport", V: "tcp"}}
	req := sip.NewRequest(sip.MESSAGE, recipient)
	req.SetDestination(s.cfg.PCSCFAddr)
	req.AppendHeader(sip.NewHeader("Content-Type", "application/vnd.3gpp.sms"))
	req.SetBody(rpData)

	res, err := transport.DoRequest(ctx, s.cfg.Client, req)
	if err != nil {
		return err
	}
	if res.StatusCode != 200 && res.StatusCode != 202 {
		return fmt.Errorf("MESSAGE 失败，状态码 %d", res.StatusCode)
	}
	return nil
}

// Status 查询投递状态。
func (s *SMS) Status(ctx context.Context, messageID string) (DeliveryStatus, error) {
	rec, err := s.store.Get(ctx, messageID)
	if err != nil {
		return StatusFailed, err
	}
	return rec.Status, nil
}
