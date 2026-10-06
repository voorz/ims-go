package voice

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sip/transport"
)

// NewAgent 创建语音 Agent。
func NewAgent(cfg Config) *Agent {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Agent{
		cfg:   cfg,
		log:   cfg.Logger,
		calls: make(map[string]*callActor),
	}
}

// newCallActor 创建呼叫 Actor（单 goroutine 串行）。
func (a *Agent) newCallActor(call *Call) *callActor {
	ca := &callActor{
		call: call,
		ch:   make(chan func(), 16),
		done: make(chan struct{}),
	}
	go func() {
		defer close(ca.done)
		for fn := range ca.ch {
			fn()
		}
	}()
	a.mu.Lock()
	a.calls[call.ID] = ca
	a.mu.Unlock()
	return ca
}

// do 在 Actor goroutine 中串行执行。
func (ca *callActor) do(fn func()) {
	select {
	case ca.ch <- fn:
	case <-ca.done:
	}
}

// transition 尝试状态转移（经 transitionMap 校验）。
func (a *Agent) transition(callID string, to State) error {
	a.mu.RLock()
	ca, ok := a.calls[callID]
	a.mu.RUnlock()
	if !ok {
		return fmt.Errorf("voice: 呼叫 %s 不存在", callID)
	}
	var err error
	done := make(chan struct{})
	ca.do(func() {
		defer close(done)
		from := ca.call.State
		if !CanTransition(from, to) {
			err = fmt.Errorf("voice: 非法转移 %s → %s", from, to)
			return
		}
		ca.call.State = to
		a.log.Info("呼叫状态变更", "id", callID, "from", from.String(), "to", to.String())
		if a.cfg.OnStateChange != nil {
			a.cfg.OnStateChange(callID, from, to)
		}
	})
	<-done
	return err
}

// Dial 发起呼叫（INVITE）。
func (a *Agent) Dial(ctx context.Context, to string) (string, error) {
	callID := fmt.Sprintf("call-%d", time.Now().UnixNano())
	call := &Call{
		ID:        callID,
		State:     StateInit,
		Direction: "outgoing",
		RemoteURI: to,
		At:        time.Now(),
	}
	ca := a.newCallActor(call)

	// 构造 INVITE（sipgo 对象）
	recipient := sip.Uri{Host: to}
	recipient.UriParams = sip.HeaderParams{{K: "transport", V: "tcp"}}
	req := sip.NewRequest(sip.INVITE, recipient)
	req.SetDestination(a.cfg.PCSCFAddr)
	req.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
	// 简化 SDP（实际由 WS-12 媒体层提供）
	sdp := "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 5004 RTP/AVP 0\r\n"
	req.SetBody([]byte(sdp))

	var res *sip.Response
	var err error
	done := make(chan struct{})
	ca.do(func() {
		defer close(done)
		ca.call.State = StateCalling
		res, err = transport.DoRequest(ctx, a.cfg.Client, req)
	})
	<-done
	if err != nil {
		_ = a.transition(callID, StateTerminating)
		_ = a.transition(callID, StateTerminated)
		return "", fmt.Errorf("voice: Dial 失败: %w", err)
	}
	// 根据响应更新状态
	switch {
	case res.StatusCode == 180:
		_ = a.transition(callID, StateRinging)
	case res.StatusCode == 183:
		_ = a.transition(callID, StateEarlyMedia)
	case res.StatusCode == 200:
		_ = a.transition(callID, StateConnected)
		// Session Timer（RFC 4028）：从 200 OK 解析 Session-Expires
		done2 := make(chan struct{})
		ca.do(func() {
			defer close(done2)
			if expires, refresher := parseSessionExpires(res); expires > 0 {
				a.startSessionTimer(ca.call, expires, refresher)
			} else {
				// IR.92 特例：无头则 fallback 1800，我是 refresher
				a.startSessionTimer(ca.call, 1800, "uac")
			}
		})
		<-done2
		// 发送 ACK（简化）
	default:
		_ = a.transition(callID, StateTerminating)
		_ = a.transition(callID, StateTerminated)
		return "", fmt.Errorf("voice: 呼叫失败，状态码 %d", res.StatusCode)
	}
	return callID, nil
}

// Hangup 挂断（BYE）。
func (a *Agent) Hangup(ctx context.Context, callID string) error {
	if err := a.transition(callID, StateTerminating); err != nil {
		return err
	}
	// 发送 BYE（简化：实际需 dialog 信息）
	a.mu.RLock()
	ca, ok := a.calls[callID]
	a.mu.RUnlock()
	if !ok {
		return fmt.Errorf("voice: 呼叫 %s 不存在", callID)
	}
	var err error
	done := make(chan struct{})
	ca.do(func() {
		defer close(done)
		// 停 Session Timer
		a.stopSessionTimer(ca.call)
		recipient := sip.Uri{Host: ca.call.RemoteURI}
		recipient.UriParams = sip.HeaderParams{{K: "transport", V: "tcp"}}
		req := sip.NewRequest(sip.BYE, recipient)
		req.SetDestination(a.cfg.PCSCFAddr)
		req.AppendHeader(sip.NewHeader("Content-Length", "0"))
		_, err = transport.DoRequest(ctx, a.cfg.Client, req)
		ca.call.State = StateTerminated
	})
	<-done
	a.mu.Lock()
	delete(a.calls, callID)
	close(ca.ch)
	a.mu.Unlock()
	return err
}

// Snapshot 返回只读呼叫视图。
func (a *Agent) Snapshot() []Call {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Call, 0, len(a.calls))
	for _, ca := range a.calls {
		out = append(out, *ca.call)
	}
	return out
}

// Close 关闭 Agent。
func (a *Agent) Close() {
	a.mu.Lock()
	for id, ca := range a.calls {
		close(ca.ch)
		delete(a.calls, id)
	}
	a.mu.Unlock()
}

var _ = sync.Mutex{}
