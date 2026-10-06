package voice

import (
	"context"
	"fmt"
	"strings"

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
			return // 已是目标状态（幂等）
		}

		// 1. 构造 re-INVITE（SDP 方向改写 + o= 版本递增 + QoS 重声明）
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
		// 2. 非 2xx → 发 ACK（4xx-6xx）
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			ack := call.Dialog.NewInDialogRequest(sip.ACK)
			_, _ = transport.DoRequest(ctx, a.cfg.Client, ack)
			err = fmt.Errorf("voice: re-INVITE 被拒绝，状态码 %d", res.StatusCode)
			return
		}
		// 3. 2xx → ACK
		ack := call.Dialog.NewInDialogRequest(sip.ACK)
		_, _ = transport.DoRequest(ctx, a.cfg.Client, ack)

		// 4. 更新状态
		call.LocalHold = hold

		// 5. 媒体方向联动：hold 时停发 RTP（省电+避免对端收静音包）
		// TODO: 需要 relay 句柄，P1 后续接线
		// if call.Relay != nil {
		//     call.Relay.SetSendEnabled(!hold)
		// }

		// 6. Session Timer 重启（re-INVITE 可能重协商 timer）
		if expires, refresher := parseSessionExpires(res); expires > 0 {
			call.SessionExpires = expires
			call.SessionRefresher = refresher
		}
		if call.sessionTimer != nil {
			call.sessionTimer.Stop()
		}
		call.sessionTimer = newSessionTimer(a, call, call.SessionExpires, call.SessionRefresher, call.Direction)
		call.sessionTimer.Start()

		a.log.Info("呼叫 hold 状态变更", "callID", callID, "hold", hold)
	})
	<-done
	return err
}

// buildReInvite 构造 dialog 内 re-INVITE（hold/resume）。
// Dialog 信息由 dialog 包单点拥有，禁止手工拼装（D-007）。
// SDP：方向改写 + o= 版本递增（RFC 3264）+ QoS 重声明（已建立会话）
func (a *Agent) buildReInvite(call *Call, hold bool) (*sip.Request, error) {
	if call.Dialog == nil {
		return nil, fmt.Errorf("voice: 无 dialog，无法发 re-INVITE")
	}
	req := call.Dialog.NewInDialogRequest(sip.INVITE)
	req.SetDestination(a.cfg.PCSCFAddr)

	// SDP：hold 时 sendonly，否则 sendrecv
	// o= 版本递增（RFC 3264 要求每次 offer 递增）
	// QoS：已建立会话，curr:qos remote 改为 sendrecv（不再走 precondition）
	direction := "sendrecv"
	if hold {
		direction = "sendonly"
	}
	sdp := a.buildHoldSDP(direction, call.SDP)
	req.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
	req.SetBody([]byte(sdp))

	return req, nil
}

// buildHoldSDP 构造 hold/resume 的 SDP（o= 递增 + QoS 重声明）。
func (a *Agent) buildHoldSDP(direction, prevSDP string) string {
	// 解析旧 o= 行的版本并递增
	version := 0
	for _, line := range strings.Split(prevSDP, "\r\n") {
		if strings.HasPrefix(line, "o=") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				fmt.Sscanf(parts[2], "%d", &version)
			}
		}
	}
	version++
	var sb strings.Builder
	sb.WriteString("v=0\r\n")
	sb.WriteString(fmt.Sprintf("o=- %d %d IN IP4 127.0.0.1\r\n", version, version))
	sb.WriteString("s=-\r\n")
	sb.WriteString("c=IN IP4 127.0.0.1\r\n")
	sb.WriteString("t=0 0\r\n")
	sb.WriteString("m=audio 5004 RTP/AVP 0\r\n")
	sb.WriteString(fmt.Sprintf("a=%s\r\n", direction))
	// QoS 重声明：已建立会话，remote 已 sendrecv
	sb.WriteString("a=curr:qos local sendrecv\r\n")
	sb.WriteString("a=curr:qos remote sendrecv\r\n")
	sb.WriteString("a=des:qos mandatory local sendrecv\r\n")
	sb.WriteString("a=des:qos optional remote sendrecv\r\n")
	return sb.String()
}

// Refer 呼转（RFC 3515）：REFER + Replaces。
// target 是转移目标（如 sip:bob@example.com）。
