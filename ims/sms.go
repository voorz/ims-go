package ims

import (
	"context"

	"github.com/voorz/ims-go/internal/sip/sms"
	"github.com/voorz/ims-go/internal/sip/stack"
)

// 本文件集中 SMS 模块的默认装配（WS-8）：
// ims.SMSConfig（公开）→ internal/sip/sms.Config（内部）的单处映射（D-007）。

// newDefaultSMS 创建默认 SMS 模块（需 SIP 栈已装配）。
// Store 为 nil 时内部用内存实现。
func newDefaultSMS(cfg Config, sipMod Module) (SMSModule, error) {
	st, ok := sipMod.(*stack.Stack)
	if !ok {
		return nil, errSMSNoSIP
	}
	sc := sms.Config{
		IMPU:      cfg.SIP.IMPU,
		PCSCFAddr: cfg.SIP.PCSCFAddrs[0],
		Client:    st.SIPClient(),
		Server:    st.SIPServer(),
		Store:     toInternalStore(cfg.SMS.Store),
	}
	return &smsModuleAdapter{inner: sms.New(sc)}, nil
}

// smsModuleAdapter 将 internal sms.SMS 适配为公开 SMSModule。
// sms.SMS 无生命周期（依附 SIP 栈），Start/Stop 为空操作。
type smsModuleAdapter struct {
	inner *sms.SMS
}

func (a *smsModuleAdapter) Start(ctx context.Context) error { return nil }
func (a *smsModuleAdapter) Stop() error                     { return nil }
func (a *smsModuleAdapter) Send(ctx context.Context, req SMSRequest) (*SMSResult, error) {
	id, err := a.inner.SendWithEncoding(ctx, req.To, req.Text, req.Encoding)
	if err != nil {
		return nil, err
	}
	return &SMSResult{MessageID: id}, nil
}

// deliveryStoreAdapter 将公开 SMSDeliveryStore 适配为内部 sms.DeliveryStore。
type deliveryStoreAdapter struct {
	s SMSDeliveryStore
}

func (a *deliveryStoreAdapter) Save(ctx context.Context, rec sms.DeliveryRecord) error {
	return a.s.Save(ctx, SMSDeliveryRecord{
		MessageID: rec.MessageID,
		To:        rec.To,
		Status:    SMSDeliveryStatus(rec.Status),
		Attempts:  rec.Attempts,
		At:        rec.At,
		Error:     rec.Error,
	})
}

func (a *deliveryStoreAdapter) Get(ctx context.Context, messageID string) (sms.DeliveryRecord, error) {
	rec, err := a.s.Get(ctx, messageID)
	if err != nil {
		return sms.DeliveryRecord{}, err
	}
	return sms.DeliveryRecord{
		MessageID: rec.MessageID,
		To:        rec.To,
		Status:    sms.DeliveryStatus(rec.Status),
		Attempts:  rec.Attempts,
		At:        rec.At,
		Error:     rec.Error,
	}, nil
}

func (a *deliveryStoreAdapter) UpdateStatus(ctx context.Context, messageID string, status sms.DeliveryStatus, errMsg string) error {
	return a.s.UpdateStatus(ctx, messageID, SMSDeliveryStatus(status), errMsg)
}

// toInternalStore 转换（nil 安全）。
func toInternalStore(s SMSDeliveryStore) sms.DeliveryStore {
	if s == nil {
		return nil
	}
	return &deliveryStoreAdapter{s: s}
}
