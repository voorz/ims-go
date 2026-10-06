package register

import (
	"context"
	"fmt"
	"time"

	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sip/transport"
)

// startRefresh 启动刷新定时器。
//
// vowifi-core 生产做法：expires×80% 时发 protected REGISTER（轻量，复用认证），
// 不是重走完整流程。401 则触发完整重注册。
func (r *Registrar) startRefresh(expiresSec int) {
	r.stopRefresh()

	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()

	// 80% 时刷新（vowifi-core 生产值），至少 30 秒
	interval := time.Duration(float64(expiresSec)*0.8) * time.Second
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.log.Info("刷新注册", "interval", interval)
				if err := r.refresh(ctx); err != nil {
					r.log.Error("刷新注册失败", "error", err)
					// vowifi-core: 刷新失败不直接判死，尝试完整重注册
					if rerr := r.Register(ctx); rerr != nil {
						r.log.Error("重注册失败", "error", rerr)
						r.setState(StateFailed)
						return
					}
				}
			}
		}
	}()
}

// refresh 执行一次 protected 刷新 REGISTER。
//
// Protected REGISTER：复用已建立的认证（Call-ID 稳定、CSeq 递增、带上次的
// Authorization），不是重走 401 挑战流程。轻量，网络开销小。
func (r *Registrar) refresh(ctx context.Context) error {
	r.mu.RLock()
	reg := r.reg
	callID := r.lastCallID
	cseq := r.lastCSeq
	auth := r.lastAuth
	r.mu.RUnlock()
	if reg == nil {
		return fmt.Errorf("未注册，无法刷新")
	}
	if callID == "" {
		return fmt.Errorf("无 dialog 状态，无法 protected 刷新")
	}

	addr := r.orderedAddrs()
	if len(addr) == 0 {
		return fmt.Errorf("无可用 P-CSCF")
	}

	// 构造 protected REGISTER：同 Call-ID，CSeq 递增，复用 Authorization
	req := r.buildRegister(r.cfg.Expires, addr[0], Variant{Name: "refresh"})
	// 替换 Call-ID 为保存的
	req.RemoveHeader("Call-ID")
	req.AppendHeader(sip.NewHeader("Call-ID", callID))
	// CSeq 递增
	req.RemoveHeader("CSeq")
	req.AppendHeader(sip.NewHeader("CSeq", fmt.Sprintf("%d REGISTER", cseq+1)))
	// 复用 Authorization（protected）
	if auth != "" {
		req.RemoveHeader("Authorization")
		req.AppendHeader(sip.NewHeader("Authorization", auth))
	}

	res, err := transport.DoRequest(ctx, r.cfg.Client, req)
	if err != nil {
		return fmt.Errorf("刷新 REGISTER 失败: %w", err)
	}
	if res.StatusCode == 401 || res.StatusCode == 407 {
		// 401：认证失效，触发完整重注册
		r.log.Info("刷新遇到 401，触发完整重注册")
		// 清除保存的状态，下次走完整流程
		r.mu.Lock()
		r.lastCallID = ""
		r.lastAuth = ""
		r.mu.Unlock()
		return r.Register(ctx)
	}
	if res.StatusCode != 200 {
		return fmt.Errorf("刷新失败，状态码 %d", res.StatusCode)
	}
	// 更新 CSeq 和过期时间
	r.mu.Lock()
	r.lastCSeq = cseq + 1
	r.mu.Unlock()
	if err := r.onRegistered(res); err != nil {
		return err
	}
	return nil
}

// stopRefresh 停止刷新定时器。
func (r *Registrar) stopRefresh() {
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.mu.Unlock()
}

// ensure SIP import is used
var _ = sip.REGISTER
