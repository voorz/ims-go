package ims

import (
	"context"

	"github.com/voorz/ims-go/internal/sip/stack"
	"github.com/voorz/ims-go/internal/sip/ussd"
)

// 本文件集中 USSD 模块的默认装配（WS-9）：
// ims.Config（公开）→ internal/sip/ussd.Config（内部）的单处映射（D-007）。

// newDefaultUSSD 创建默认 USSD 模块（需 SIP 栈已装配）。
func newDefaultUSSD(cfg Config, sipMod Module) (USSDModule, error) {
	st, ok := sipMod.(*stack.Stack)
	if !ok {
		return nil, errUSSDNoSIP
	}
	uc := ussd.Config{
		IMPU:      cfg.SIP.IMPU,
		Domain:    cfg.SIP.HomeDomain,
		PCSCFAddr: cfg.SIP.PCSCFAddrs[0],
		Contact:   cfg.SIP.Contact,
		Client:    st.SIPClient(),
		Server:    st.SIPServer(),
	}
	return &ussdModuleAdapter{inner: ussd.NewService(uc)}, nil
}

// ussdModuleAdapter 将 internal ussd.Service 适配为公开 USSDModule。
// ussd.Service 无生命周期（依附 SIP 栈），Start/Stop 为空操作。
type ussdModuleAdapter struct {
	inner *ussd.Service
}

func (a *ussdModuleAdapter) Start(ctx context.Context) error { return nil }
func (a *ussdModuleAdapter) Stop() error                     { return nil }

func (a *ussdModuleAdapter) Send(ctx context.Context, code string) (*USSDResult, error) {
	r, err := a.inner.Send(ctx, code)
	if err != nil {
		return nil, err
	}
	return toPublicUSSDResult(r), nil
}

func (a *ussdModuleAdapter) Continue(ctx context.Context, input string) (*USSDResult, error) {
	r, err := a.inner.Continue(ctx, input)
	if err != nil {
		return nil, err
	}
	return toPublicUSSDResult(r), nil
}

func (a *ussdModuleAdapter) Cancel(ctx context.Context) error {
	return a.inner.Cancel(ctx)
}

// toPublicUSSDResult 转换内部 Result 为公开 USSDResult。
// 内部 Status=1（需继续）映射为 HasMore=true。
func toPublicUSSDResult(r *ussd.Result) *USSDResult {
	return &USSDResult{
		SessionID: r.SessionID,
		Text:      r.Text,
		HasMore:   r.Status == 1,
	}
}
