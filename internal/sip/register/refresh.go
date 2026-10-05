package register

import (
	"context"
	"time"
)

// startRefresh 启动刷新定时器：在过期前一半时间重注册。
func (r *Registrar) startRefresh(expiresSec int) {
	r.stopRefresh()

	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()

	// 过期前 50% 时刷新，至少 30 秒
	interval := time.Duration(expiresSec/2) * time.Second
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
				r.log.Info("刷新注册")
				if err := r.refresh(ctx); err != nil {
					r.log.Error("刷新注册失败", "error", err)
					r.setState(StateFailed)
					return
				}
			}
		}
	}()
}

// refresh 执行一次刷新 REGISTER（带当前认证信息）。
func (r *Registrar) refresh(ctx context.Context) error {
	// 刷新是已认证的 REGISTER（简化：重新走完整流程）
	// 实际应复用 nonce；这里为简化重新认证
	return r.Register(ctx)
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
