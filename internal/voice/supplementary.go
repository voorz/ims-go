package voice

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sip/transport"
)

// Hold 保持呼叫（re-INVITE，SDP 改为 sendonly）。
// TS 24.610：通过 re-INVITE 改变 SDP 方向实现 hold。
func (a *Agent) Hold(ctx context.Context, callID string) error {
	return a.setHold(ctx, callID, true)
}

// Resume 恢复呼叫（re-INVITE，SDP 改回 sendrecv）。
func (a *Agent) Resume(ctx context.Context, callID string) error {
	return a.setHold(ctx, callID, false)
}

// setHold 通过 re-INVITE 设置 hold 状态。
func (a *Agent) setHold(ctx context.Context, callID string, hold bool) error {
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
		call := ca.call
		if call.State != StateConnected {
			err = fmt.Errorf("voice: 呼叫状态 %s，无法 hold/resume", call.State)
			return
		}
		if call.LocalHold == hold {
			return // 已是目标状态
		}

		// 构造 re-INVITE（dialog 内，CSeq 递增）
		req, buildErr := a.buildReInvite(call, hold)
		if buildErr != nil {
			err = buildErr
			return
		}
		res, doErr := transport.DoRequest(ctx, a.cfg.Client, req)
		if doErr != nil {
			err = fmt.Errorf("voice: re-INVITE 失败: %w", doErr)
			return
		}
		if res.StatusCode != 200 {
			err = fmt.Errorf("voice: re-INVITE 被拒绝，状态码 %d", res.StatusCode)
			return
		}
		call.LocalHold = hold
		a.log.Info("呼叫 hold 状态变更", "callID", callID, "hold", hold)
	})
	<-done
	return err
}

// buildReInvite 构造 dialog 内 re-INVITE（hold/resume）。
func (a *Agent) buildReInvite(call *Call, hold bool) (*sip.Request, error) {
	recipient := sip.Uri{}
	if err := sip.ParseUri(call.RemoteTarget, &recipient); err != nil {
		return nil, fmt.Errorf("voice: 解析 RemoteTarget: %w", err)
	}
	req := sip.NewRequest(sip.INVITE, recipient)
	req.SetDestination(a.cfg.PCSCFAddr)

	req.AppendHeader(sip.NewHeader("Call-ID", call.CallID))
	call.CSeq++
	req.AppendHeader(sip.NewHeader("CSeq", fmt.Sprintf("%d INVITE", call.CSeq)))

	from := &sip.FromHeader{
		Address: sip.Uri{User: a.cfg.IMPU},
		Params:  sip.HeaderParams{{K: "tag", V: call.LocalTag}},
	}
	req.AppendHeader(from)
	to := &sip.ToHeader{
		Address: recipient,
		Params:  sip.HeaderParams{{K: "tag", V: call.RemoteTag}},
	}
	req.AppendHeader(to)

	// SDP：hold 时 sendonly，否则 sendrecv
	direction := "sendrecv"
	if hold {
		direction = "sendonly"
	}
	sdp := a.buildHoldSDP(direction)
	req.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
	req.SetBody([]byte(sdp))

	return req, nil
}

// buildHoldSDP 构造 hold/resume 的 SDP。
func (a *Agent) buildHoldSDP(direction string) string {
	return fmt.Sprintf("v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 5004 RTP/AVP 0\r\na=%s\r\n", direction)
}

// Refer 呼转（RFC 3515）：REFER + Replaces。
// target 是转移目标（如 sip:bob@example.com）。
func (a *Agent) Refer(ctx context.Context, callID, target string) error {
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
		call := ca.call
		if call.State != StateConnected {
			err = fmt.Errorf("voice: 呼叫状态 %s，无法转移", call.State)
			return
		}

		recipient := sip.Uri{}
		if parseErr := sip.ParseUri(call.RemoteTarget, &recipient); parseErr != nil {
			err = parseErr
			return
		}
		req := sip.NewRequest(sip.REFER, recipient)
		req.SetDestination(a.cfg.PCSCFAddr)

		req.AppendHeader(sip.NewHeader("Call-ID", call.CallID))
		call.CSeq++
		req.AppendHeader(sip.NewHeader("CSeq", fmt.Sprintf("%d REFER", call.CSeq)))

		from := &sip.FromHeader{
			Address: sip.Uri{User: a.cfg.IMPU},
			Params:  sip.HeaderParams{{K: "tag", V: call.LocalTag}},
		}
		req.AppendHeader(from)
		to := &sip.ToHeader{
			Address: recipient,
			Params:  sip.HeaderParams{{K: "tag", V: call.RemoteTag}},
		}
		req.AppendHeader(to)

		// Refer-To
		req.AppendHeader(sip.NewHeader("Refer-To", target))
		// Referred-By（可选）
		req.AppendHeader(sip.NewHeader("Referred-By", fmt.Sprintf("<sip:%s>", a.cfg.IMPU)))
		req.AppendHeader(sip.NewHeader("Content-Length", "0"))

		res, doErr := transport.DoRequest(ctx, a.cfg.Client, req)
		if doErr != nil {
			err = fmt.Errorf("voice: REFER 失败: %w", doErr)
			return
		}
		// 202 Accepted 表示转移已接受（异步通过 NOTIFY 报告结果）
		if res.StatusCode != 202 && res.StatusCode != 200 {
			err = fmt.Errorf("voice: REFER 被拒绝，状态码 %d", res.StatusCode)
			return
		}
		a.log.Info("呼叫转移已发起", "callID", callID, "target", target)
	})
	<-done
	return err
}

// startSessionTimer 启动 Session Timer（RFC 4028）。
// expires 秒；refresher 为 "uac"（我是刷新方）或 "uas"（对端是刷新方）。
func (a *Agent) startSessionTimer(call *Call, expires int, refresher string) {
	if expires <= 0 {
		return
	}
	// vowifi-go 经验：无 Session-Expires 头时 fallback 1800（IR.92）。
	if expires == 0 {
		expires = 1800
	}
	call.SessionExpires = expires
	call.SessionRefresher = refresher

	if call.SessionTimer != nil {
		call.SessionTimer.Stop()
	}

	if refresher == "uac" {
		// 我是 refresher：expires/2 时发 UPDATE 刷新。
		// 失败只 warn，不杀呼叫（vowifi-go 生产经验）。
		interval := time.Duration(expires/2) * time.Second
		call.SessionTimer = time.AfterFunc(interval, func() {
			a.log.Info("Session Timer 刷新", "callID", call.ID)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// 简化：用 re-INVITE 刷新（实际可用 UPDATE）
			if err := a.setHold(ctx, call.ID, call.LocalHold); err != nil {
				a.log.Warn("Session 刷新失败（呼叫继续）", "error", err)
			} else {
				// 刷新成功，重启 timer
				a.mu.RLock()
				ca, ok := a.calls[call.ID]
				a.mu.RUnlock()
				if ok {
					ca.do(func() {
						a.startSessionTimer(ca.call, expires, refresher)
					})
				}
			}
		})
	} else {
		// 对端是 refresher：超时未刷新则 Hangup（死呼叫检测最后防线）。
		interval := time.Duration(expires) * time.Second
		call.SessionTimer = time.AfterFunc(interval, func() {
			a.log.Warn("Session Timer 超时，对端未刷新，挂断", "callID", call.ID)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = a.Hangup(ctx, call.ID)
		})
	}
}

// stopSessionTimer 停止 Session Timer。
func (a *Agent) stopSessionTimer(call *Call) {
	if call.SessionTimer != nil {
		call.SessionTimer.Stop()
		call.SessionTimer = nil
	}
}

// parseSessionExpires 从响应解析 Session-Expires 头。
func parseSessionExpires(res *sip.Response) (expires int, refresher string) {
	h := res.GetHeader("Session-Expires")
	if h == nil {
		return 0, ""
	}
	val := h.Value()
	// 格式：1800;refresher=uac
	parts := strings.Split(val, ";")
	fmt.Sscanf(strings.TrimSpace(parts[0]), "%d", &expires)
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "refresher=") {
			refresher = strings.TrimPrefix(p, "refresher=")
		}
	}
	return expires, refresher
}
