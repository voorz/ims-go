package voice

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/emergency"
	"github.com/voorz/ims-go/internal/sip/dialog"
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
	// RFC 3262：声明支持可靠临时响应
	req.AppendHeader(sip.NewHeader("Supported", "100rel"))
	// RFC 3312：precondition（Wi-Fi 资源恒可用，走形式满足 IR.51）
	req.AppendHeader(sip.NewHeader("Require", "precondition"))
	req.AppendHeader(sip.NewHeader("Supported", "precondition"))
	// 紧急呼叫：Priority: emergency（TS 24.229）
	if strings.HasPrefix(strings.ToLower(to), "urn:service:sos") {
		req.AppendHeader(sip.NewHeader("Priority", "emergency"))
	}
	// SDP（含 precondition 属性，对象模型构造）
	sdp := a.buildInviteSDP()
	req.SetBody([]byte(sdp))

	var res *sip.Response
	var err error
	done := make(chan struct{})
	ca.do(func() {
		defer close(done)
		ca.call.State = StateCalling
		// No-answer timer（60s）：超时未接通则挂断
		// vowifi-go 生产经验：只在 Calling/Ringing 触发
		timeout := 60 * time.Second
		if a.cfg.NoAnswerTimeout > 0 {
			timeout = a.cfg.NoAnswerTimeout
		}
		ca.call.noAnswerTimer = time.AfterFunc(timeout, func() {
			a.mu.RLock()
			ca2, ok := a.calls[callID]
			a.mu.RUnlock()
			if ok {
				ca2.do(func() {
					if ca2.call.State == StateCalling || ca2.call.State == StateRinging {
						a.log.Warn("呼叫未接听超时，挂断", "callID", callID)
						ctx2, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						_ = a.Hangup(ctx2, callID)
					}
				})
			}
		})
		// INVITE 发送（含 401/407 Digest-AKA 鉴权 + PRACK 闭环 A2-1）
		res, err = a.doInviteWithAuth(ctx, req, call)
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
		// P0：从 2xx 学习 dialog（不可绕过，失败则挂断）
		// Dialog 由 internal/sip/dialog 单点拥有，voice 只持句柄
		localTag, _ := req.From().Params.Get("tag")
		dlg := &dialog.Dialog{
			ID: dialog.ID{
				CallID:   req.CallID().Value(),
				LocalTag: localTag,
			},
			RemoteURI: recipient,
			LocalURI:  sip.Uri{User: a.cfg.IMPU},
		}
		if err := dlg.LearnFromResponse(res); err != nil {
			_ = a.transition(callID, StateTerminating)
			_ = a.transition(callID, StateTerminated)
			return "", fmt.Errorf("voice: dialog 学习失败: %w", err)
		}
		done2 := make(chan struct{})
		ca.do(func() {
			defer close(done2)
			ca.call.Dialog = dlg
			// 停 no-answer timer（已接通）
			if ca.call.noAnswerTimer != nil {
				ca.call.noAnswerTimer.Stop()
				ca.call.noAnswerTimer = nil
			}
			// 2xx 才发 ACK（RFC 3261：非 2xx 的 ACK 由事务层发，避免双 ACK）
			ack := dlg.NewInDialogRequest(sip.ACK)
			_, _ = transport.DoRequest(ctx, a.cfg.Client, ack)
			// 直接赋值（已在 Actor 内，避免 transition 死锁）
			// 手动触发 OnStateChange（transition 的副作用）
			from := ca.call.State
			ca.call.State = StateConnected
			if a.cfg.OnStateChange != nil {
				a.cfg.OnStateChange(callID, from, StateConnected)
			}
			// Session Timer（RFC 4028）：显式状态机
			expires, refresher := parseSessionExpires(res)
			ca.call.SessionExpires = expires
			ca.call.SessionRefresher = refresher
			ca.call.sessionTimer = newSessionTimer(a, ca.call, expires, refresher, "outgoing")
			ca.call.sessionTimer.Start()
		})
		<-done2
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
		// 幂等释放：防止 CANCEL/BYE/超时三路并发重复释放（vowifi-go 生产经验）
		ca.call.finalizeOnce.Do(func() {
			// 停 Session Timer
			if ca.call.sessionTimer != nil {
				ca.call.sessionTimer.Stop()
			}
			// 停 no-answer timer
			if ca.call.noAnswerTimer != nil {
				ca.call.noAnswerTimer.Stop()
			}
			if ca.call.Dialog == nil {
				err = fmt.Errorf("voice: 无 dialog，无法发 BYE")
				return
			}
			req := ca.call.Dialog.NewInDialogRequest(sip.BYE)
			req.SetDestination(a.cfg.PCSCFAddr)
			_, err = transport.DoRequest(ctx, a.cfg.Client, req)
			ca.call.State = StateTerminated
		})
	})
	<-done
	a.mu.Lock()
	delete(a.calls, callID)
	// 注意：不 close(ca.ch)，避免 do() 并发发送 panic（vowifi-go 坑）
	// ch 由 GC 回收，或显式 Close 时处理
	a.mu.Unlock()
	return err
}

// Snapshot 返回只读呼叫视图。
func (a *Agent) Snapshot() []Call {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Call, 0, len(a.calls))
	for _, ca := range a.calls {
		// 浅拷贝：跳过 sync.Once（不可复制）
		c := ca.call
		out = append(out, Call{
			ID:               c.ID,
			State:            c.State,
			Direction:        c.Direction,
			RemoteURI:        c.RemoteURI,
			At:               c.At,
			Dialog:           c.Dialog,
			LocalHold:        c.LocalHold,
			RemoteHold:       c.RemoteHold,
			SessionExpires:   c.SessionExpires,
			SessionRefresher: c.SessionRefresher,
			SDP:              c.SDP,
			RemoteSDP:        c.RemoteSDP,
		})
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

// Answer 接听来电（vowifi-go 铁律：无 SDP 不接通）。
// sdp 为本地 SDP（应答）；空则返回错误。
// A2-2：使用保存的 inbound tx 真正发送 200 OK。
func (a *Agent) Answer(ctx context.Context, callID, sdp string) error {
	if sdp == "" {
		return fmt.Errorf("voice: 接听需要 SDP（无媒体不接通）")
	}
	ca, ok := a.calls[callID]
	if !ok {
		return fmt.Errorf("voice: 呼叫 %s 不存在", callID)
	}
	var err error
	done := make(chan struct{})
	ca.do(func() {
		defer close(done)
		call := ca.call
		if call.State != StateRinging && call.State != StateEarlyMedia {
			err = fmt.Errorf("voice: 呼叫状态 %s，无法接听", call.State)
			return
		}
		// A2-2：用保存的 inbound tx 发 200 OK
		if call.inboundTx == nil || call.inboundReq == nil {
			err = fmt.Errorf("voice: 入站事务丢失，无法回 200 OK")
			return
		}
		res := sip.NewResponseFromRequest(call.inboundReq, 200, "OK", nil)
		res.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
		res.SetBody([]byte(sdp))
		// Contact 头（dialog 需要）
		if contact := a.buildContactHeader(); contact != "" {
			res.AppendHeader(sip.NewHeader("Contact", contact))
		}
		if rerr := call.inboundTx.Respond(res); rerr != nil {
			err = fmt.Errorf("voice: 发送 200 OK 失败: %w", rerr)
			return
		}
		call.SDP = sdp
		call.State = StateConnected
		// 从 200 OK 学习 dialog（To tag）
		a.learnDialogFrom200(call, res)
		a.log.Info("呼叫已接听（200 OK 已发送）", "callID", callID)
	})
	<-done
	return err
}

// buildContactHeader 构造 Contact 头（dialog 用）。
func (a *Agent) buildContactHeader() string {
	if a.cfg.IMPU != "" {
		return "<" + a.cfg.IMPU + ">"
	}
	return ""
}

// learnDialogFrom200 从 200 OK 响应学习 dialog（To tag）。
func (a *Agent) learnDialogFrom200(call *Call, res *sip.Response) {
	// 提取 To tag 建立 dialog（简化版，完整实现后续）
	a.log.Debug("学习 dialog", "callID", call.ID)
}

// Reject 拒绝入站呼叫（486 Busy Here 或其他状态码）。
func (a *Agent) Reject(ctx context.Context, callID string, statusCode int, reason string) error {
	ca, ok := a.calls[callID]
	if !ok {
		return fmt.Errorf("voice: 呼叫 %s 不存在", callID)
	}
	var err error
	done := make(chan struct{})
	ca.do(func() {
		defer close(done)
		call := ca.call
		if call.inboundTx == nil || call.inboundReq == nil {
			err = fmt.Errorf("voice: 入站事务丢失，无法拒绝")
			return
		}
		if statusCode < 400 || statusCode >= 700 {
			statusCode = 486
		}
		if reason == "" {
			reason = "Busy Here"
		}
		res := sip.NewResponseFromRequest(call.inboundReq, statusCode, reason, nil)
		if rerr := call.inboundTx.Respond(res); rerr != nil {
			err = fmt.Errorf("voice: 发送拒绝响应失败: %w", rerr)
			return
		}
		call.State = StateTerminated
		a.log.Info("呼叫已拒绝", "callID", callID, "status", statusCode)
	})
	<-done
	return err
}

// DialEmergency 拨打紧急呼叫（3GPP TS 24.229）。
// 使用 urn:service:sos + Priority: emergency 头。
// 紧急呼叫不要求预先 REGISTER（网络必须接受）。
func (a *Agent) DialEmergency(ctx context.Context, destination string) (string, error) {
	urn := emergency.ServiceURNFor(destination)
	if urn == "" {
		return "", fmt.Errorf("voice: 非紧急号码: %s", destination)
	}
	// 紧急呼叫：直接 Dial 到 URN，带 emergency 优先级
	callID, err := a.Dial(ctx, urn)
	if err != nil {
		return "", err
	}
	// 标记为紧急（通过 Call 的 RemoteURI 已是 URN，状态机同普通呼叫）
	a.mu.RLock()
	ca, ok := a.calls[callID]
	a.mu.RUnlock()
	if ok {
		ca.do(func() {
			// 紧急呼叫不启动 no-answer timer（网络侧会处理）
			if ca.call.noAnswerTimer != nil {
				ca.call.noAnswerTimer.Stop()
				ca.call.noAnswerTimer = nil
			}
		})
	}
	return callID, nil
}

// SetMediaAddr 设置媒体地址（隧道建立后调用）。
// localIP 是隧道内 IP（SDP 的 c= 行）；rtpPort 是 RTP 端口（0 用默认值）。
// 必须在 Dial 前调用，否则 SDP 用 fallback 地址。
func (a *Agent) SetMediaAddr(localIP string, rtpPort int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// 更新所有未来呼叫的默认配置
	// 注意：进行中的呼叫不受影响（SDP 已发送）
	a.cfg.LocalIP = localIP
	a.cfg.RTPPort = rtpPort
}
