package register

import (
	"context"
	"fmt"
	"strings"

	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sip/transport"
)

// EmergencyRegister 紧急注册（3GPP TS 24.229）。
// 用于无有效凭证但需紧急呼叫的场景：
//   - 跳过 AKA 认证（网络必须接受紧急注册）
//   - 只用 base 变体（不试错）
//   - Contact 带 sos 参数指示紧急
//
// 注意：多数 VoWiFi 场景用已有普通注册打紧急呼叫即可；
// 此方法仅用于无注册时的紧急兜底。
func (r *Registrar) EmergencyRegister(ctx context.Context) error {
	r.setState(StateRegistering)

	addrs := r.orderedAddrs()
	if len(addrs) == 0 {
		r.setState(StateFailed)
		return fmt.Errorf("register: 无可用 P-CSCF")
	}

	var lastErr error
	for _, addr := range addrs {
		res, err := r.attemptEmergency(ctx, addr)
		if err != nil {
			r.penalize(addr)
			lastErr = err
			continue
		}
		if res.StatusCode == 200 {
			if err := r.onRegistered(res); err != nil {
				r.setState(StateFailed)
				return err
			}
			r.log.Info("紧急注册成功", "addr", addr)
			return nil
		}
		lastErr = fmt.Errorf("紧急注册失败: %d", res.StatusCode)
		r.penalize(addr)
	}
	r.setState(StateFailed)
	if lastErr != nil {
		return fmt.Errorf("register: 紧急注册失败: %w", lastErr)
	}
	return fmt.Errorf("register: 紧急注册失败")
}

// attemptEmergency 单次紧急 REGISTER 尝试（无 AKA）。
func (r *Registrar) attemptEmergency(ctx context.Context, addr string) (*sip.Response, error) {
	// 用 base 变体但 InitialAuth=none（明确不带认证头）
	v := Variant{Name: "emergency", InitialAuth: "none"}
	req := r.buildRegister(600, addr, v)
	// Contact 添加 sos 参数（紧急指示，TS 24.229）
	if h := req.GetHeader("Contact"); h != nil {
		val := h.Value()
		if !strings.Contains(val, "sos") {
			req.RemoveHeader("Contact")
			val = strings.Replace(val, ">", ";sos>", 1)
			req.AppendHeader(sip.NewHeader("Contact", val))
		}
	}
	return transport.DoRequest(ctx, r.cfg.Client, req)
}
