package voice

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sip/transport"
)

// REFER（RFC 3515）三阶段状态机：
//   referSent → accepted(202) → notified(sipfrag 2xx/failed)
//
// 202 只表示"收到转移请求"，转移成败在 NOTIFY 的 sipfrag body 里。
// NOTIFY 超时（30s）转失败。

type referState int

const (
	referIdle referState = iota
	referSent
	referAccepted
	referNotified
	referFailed
)

// ReferSession 跟踪一次 REFER 转移。
type referSession struct {
	mu     sync.Mutex
	callID string
	target string
	state  referState
	result chan referResult
	timer  *time.Timer
}

type referResult struct {
	success bool
	sipCode int
}

// newReferSession 创建转移会话。
func newReferSession(callID, target string) *referSession {
	return &referSession{
		callID: callID,
		target: target,
		state:  referIdle,
		result: make(chan referResult, 1),
	}
}

// Refer 发起呼叫转移（blind，自动降级）。
// consultative（带 Replaces）失败时自动降为 blind。
func (a *Agent) Refer(ctx context.Context, callID, target string) error {
	// 先尝试 consultative（如果 target 含 Replaces 信息）
	// 简化：直接 blind，失败时由调用方决定
	return a.referBlind(ctx, callID, target)
}

// referBlind blind transfer。
func (a *Agent) referBlind(ctx context.Context, callID, target string) error {
	ca, ok := a.calls[callID]
	if !ok {
		return fmt.Errorf("voice: 呼叫 %s 不存在", callID)
	}
	session := newReferSession(callID, target)

	var err error
	done := make(chan struct{})
	ca.do(func() {
		defer close(done)
		call := ca.call
		if call.State != StateConnected {
			err = fmt.Errorf("voice: 呼叫状态 %s，无法转移", call.State)
			return
		}
		if call.Dialog == nil {
			err = fmt.Errorf("voice: 无 dialog，无法发 REFER")
			return
		}
		req := call.Dialog.NewInDialogRequest(sip.REFER)
		req.SetDestination(a.cfg.PCSCFAddr)
		req.AppendHeader(sip.NewHeader("Refer-To", target))
		req.AppendHeader(sip.NewHeader("Referred-By", fmt.Sprintf("<sip:%s>", a.cfg.IMPU)))

		session.mu.Lock()
		session.state = referSent
		session.mu.Unlock()

		res, doErr := transport.DoRequest(ctx, a.cfg.Client, req)
		if doErr != nil {
			err = fmt.Errorf("voice: REFER 失败: %w", doErr)
			return
		}
		// 202 Accepted → 等 NOTIFY
		if res.StatusCode != 202 && res.StatusCode != 200 {
			// 403/420/501 → 可降级（consultative 场景）
			if res.StatusCode == 403 || res.StatusCode == 420 || res.StatusCode == 501 {
				err = fmt.Errorf("voice: REFER 被拒 %d（可降级为 blind）: %w", res.StatusCode, ErrReferDowngrade)
			} else {
				err = fmt.Errorf("voice: REFER 被拒绝，状态码 %d", res.StatusCode)
			}
			return
		}
		session.mu.Lock()
		session.state = referAccepted
		session.mu.Unlock()
		a.log.Info("REFER 已接受，等待 NOTIFY", "callID", callID)

		// 启动 NOTIFY 超时（30s）
		session.timer = time.AfterFunc(30*time.Second, func() {
			session.mu.Lock()
			if session.state == referAccepted {
				session.state = referFailed
				session.result <- referResult{success: false}
			}
			session.mu.Unlock()
		})
		// 注册到 agent 的 refer 会话表（NOTIFY 到达时查找）
		a.mu.Lock()
		if a.referSessions == nil {
			a.referSessions = make(map[string]*referSession)
		}
		a.referSessions[callID] = session
		a.mu.Unlock()
	})
	<-done
	if err != nil {
		return err
	}
	// 等待 NOTIFY 结果
	select {
	case r := <-session.result:
		if !r.success {
			return fmt.Errorf("voice: 转移失败，sipfrag %d", r.sipCode)
		}
		a.log.Info("呼叫转移成功", "callID", callID, "target", target)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// handleReferNotify 处理 REFER 的 NOTIFY（sipfrag）。
// 由 inbound dispatcher 调用。
func (a *Agent) handleReferNotify(callID string, sipfrag string) {
	a.mu.RLock()
	session, ok := a.referSessions[callID]
	a.mu.RUnlock()
	if !ok {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state != referAccepted {
		return
	}
	// 解析 sipfrag 首行：SIP/2.0 200
	lines := strings.Split(sipfrag, "\r\n")
	if len(lines) > 0 {
		parts := strings.Fields(lines[0])
		if len(parts) >= 2 {
			var code int
			fmt.Sscanf(parts[1], "%d", &code)
			if code >= 200 && code < 300 {
				session.state = referNotified
				if session.timer != nil {
					session.timer.Stop()
				}
				session.result <- referResult{success: true, sipCode: code}
			} else {
				session.state = referFailed
				if session.timer != nil {
					session.timer.Stop()
				}
				session.result <- referResult{success: false, sipCode: code}
			}
		}
	}
	// 清理
	a.mu.Lock()
	delete(a.referSessions, callID)
	a.mu.Unlock()
}

// ErrReferDowngrade 表示可降级为 blind transfer。
var ErrReferDowngrade = fmt.Errorf("可降级")
