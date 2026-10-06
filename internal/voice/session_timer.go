package voice

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sip/transport"
)

// Session Timer（RFC 4028）显式状态机。
//
// 状态：idle → active → refreshing → active → ...
//   - active：timer 运行中，等待刷新时机
//   - refreshing：已发 UPDATE/re-INVITE，等待响应
//
// Refresher 方向（RFC 4028 角色）：
//   - outbound 呼叫：refresher=uac 表示我是刷新方
//   - inbound 呼叫：refresher=uas 表示我是刷新方（角色反转）

type sessionTimerState int

const (
	sessionIdle sessionTimerState = iota
	sessionActive
	sessionRefreshing
)

// SessionTimer 管理单个呼叫的会话定时器。
type sessionTimer struct {
	mu          sync.Mutex
	call        *Call
	agent       *Agent
	expires     int    // 秒
	refresher   string // "uac" / "uas"
	isRefresher bool   // 我是否是刷新方（根据 direction + refresher 计算）

	state sessionTimerState
	timer *time.Timer
	// 422 重试计数（防无限循环）
	retries422 int
}

// newSessionTimer 创建 Session Timer。
// direction: "outgoing"/"incoming"；refresher 来自 Session-Expires 头。
func newSessionTimer(agent *Agent, call *Call, expires int, refresher, direction string) *sessionTimer {
	// IR.92 fallback：无头时 1800，我是 refresher
	if expires <= 0 {
		expires = 1800
		refresher = "uac"
	}
	// 计算我是否是刷新方（RFC 4028 角色反转）
	isRefresher := false
	if direction == "outgoing" {
		isRefresher = (refresher == "uac")
	} else {
		isRefresher = (refresher == "uas")
	}
	return &sessionTimer{
		agent:       agent,
		call:        call,
		expires:     expires,
		refresher:   refresher,
		isRefresher: isRefresher,
		state:       sessionIdle,
	}
}

// Start 启动 timer。
func (st *sessionTimer) Start() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.state = sessionActive
	st.scheduleLocked()
}

// Stop 停止 timer。
func (st *sessionTimer) Stop() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.state = sessionIdle
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
}

// scheduleLocked 安排下一次触发（调用方持锁）。
func (st *sessionTimer) scheduleLocked() {
	if st.timer != nil {
		st.timer.Stop()
	}
	var interval time.Duration
	if st.isRefresher {
		// 我是刷新方：half ≥90s 用 half，否则 expires-10s
		half := st.expires / 2
		if half >= 90 {
			interval = time.Duration(half) * time.Second
		} else {
			interval = time.Duration(st.expires-10) * time.Second
		}
	} else {
		// 对端是刷新方：超时未刷新则挂断（死呼叫最后防线）
		interval = time.Duration(st.expires) * time.Second
	}
	st.timer = time.AfterFunc(interval, st.onExpire)
}

// onExpire timer 触发。
func (st *sessionTimer) onExpire() {
	st.mu.Lock()
	if st.state != sessionActive {
		st.mu.Unlock()
		return
	}
	st.state = sessionRefreshing
	st.mu.Unlock()

	if st.isRefresher {
		st.refresh()
	} else {
		// 对端未刷新：挂断
		st.agent.log.Warn("Session Timer 超时，对端未刷新，挂断", "callID", st.call.ID)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = st.agent.Hangup(ctx, st.call.ID)
	}
}

// refresh 发送刷新请求（UPDATE 优先，405/501 降级 re-INVITE）。
func (st *sessionTimer) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 先试 UPDATE
	if err := st.sendUpdate(ctx); err != nil {
		// 405/501 → 降级 re-INVITE
		st.agent.log.Info("UPDATE 被拒，降级 re-INVITE", "callID", st.call.ID, "error", err)
		if err2 := st.sendReInvite(ctx); err2 != nil {
			st.agent.log.Warn("Session 刷新失败（呼叫继续）", "callID", st.call.ID, "error", err2)
			st.reschedule()
			return
		}
	}
	st.onRefreshSuccess()
}

// sendUpdate 发 UPDATE 刷新。
func (st *sessionTimer) sendUpdate(ctx context.Context) error {
	if st.call.Dialog == nil {
		return fmt.Errorf("无 dialog")
	}
	req := st.call.Dialog.NewInDialogRequest(sip.UPDATE)
	// Session-Expires 头
	req.AppendHeader(sip.NewHeader("Session-Expires",
		fmt.Sprintf("%d;refresher=%s", st.expires, st.refresher)))
	res, err := transport.DoRequest(ctx, st.agent.cfg.Client, req)
	if err != nil {
		return err
	}
	// 422 → 解析 Min-SE，调整后重试（防无限循环）
	if res.StatusCode == 422 && st.retries422 < 2 {
		if minSE := parseMinSE(res); minSE > 0 && minSE > st.expires {
			st.retries422++
			st.expires = minSE
			st.agent.log.Info("422 Min-SE，调整后重试", "minSE", minSE)
			return st.sendUpdate(ctx)
		}
	}
	if res.StatusCode == 405 || res.StatusCode == 501 {
		return fmt.Errorf("UPDATE 不被支持: %d", res.StatusCode)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("UPDATE 失败: %d", res.StatusCode)
	}
	return nil
}

// sendReInvite 发 re-INVITE 刷新（降级路径）。
func (st *sessionTimer) sendReInvite(ctx context.Context) error {
	if st.call.Dialog == nil {
		return fmt.Errorf("无 dialog")
	}
	req := st.call.Dialog.NewInDialogRequest(sip.INVITE)
	req.AppendHeader(sip.NewHeader("Session-Expires",
		fmt.Sprintf("%d;refresher=%s", st.expires, st.refresher)))
	// SDP 不变（仅刷新 timer）
	req.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
	req.SetBody([]byte(st.call.SDP))
	res, err := transport.DoRequest(ctx, st.agent.cfg.Client, req)
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("re-INVITE 刷新失败: %d", res.StatusCode)
	}
	// 2xx → 发 ACK
	ack := st.call.Dialog.NewInDialogRequest(sip.ACK)
	_, _ = transport.DoRequest(ctx, st.agent.cfg.Client, ack)
	return nil
}

// onRefreshSuccess 刷新成功：重启 timer。
func (st *sessionTimer) onRefreshSuccess() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.state = sessionActive
	st.retries422 = 0
	st.scheduleLocked()
	st.agent.log.Info("Session 刷新成功", "callID", st.call.ID)
}

// reschedule 刷新失败后重新安排（不杀呼叫）。
func (st *sessionTimer) reschedule() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.state = sessionActive
	st.scheduleLocked()
}

// parseSessionExpires 从响应解析 Session-Expires 头。
// 返回 (expires秒, refresher)。无头返回 (0, "")。
func parseSessionExpires(res *sip.Response) (int, string) {
	h := res.GetHeader("Session-Expires")
	if h == nil {
		return 0, ""
	}
	val := h.Value()
	parts := strings.Split(val, ";")
	expires, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
	refresher := "uac" // 默认
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "refresher=") {
			refresher = strings.TrimPrefix(p, "refresher=")
		}
	}
	return expires, refresher
}

// parseMinSE 从 422 响应解析 Min-SE 头。
func parseMinSE(res *sip.Response) int {
	h := res.GetHeader("Min-SE")
	if h == nil {
		return 0
	}
	v, _ := strconv.Atoi(strings.TrimSpace(h.Value()))
	return v
}
