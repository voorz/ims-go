package voice

import (
	"context"
	"fmt"

	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sim"
	"github.com/voorz/ims-go/internal/sip/auth"
)

// doInviteWithAuth 发送 INVITE 并处理 401/407 Digest-AKA 挑战。
// 复用 sim 包的 AKA 计算（与 REGISTER 同一套，D-015）。
// 最多 3 轮挑战（防认证循环）。
func (a *Agent) doInviteWithAuth(ctx context.Context, req *sip.Request, call *Call) (*sip.Response, error) {
	const maxRounds = 3
	for round := 0; round < maxRounds; round++ {
		res, err := a.doInviteSingle(ctx, req, call)
		if err != nil {
			return nil, err
		}
		if res.StatusCode != 401 && res.StatusCode != 407 {
			return res, nil
		}
		// 401/407：计算响应并重发
		if a.cfg.AKAProvider == nil {
			return res, fmt.Errorf("voice: 收到 %d 但无 AKAProvider", res.StatusCode)
		}
		authReq, err := a.buildAuthInvite(req, res)
		if err != nil {
			return nil, fmt.Errorf("voice: 构造鉴权 INVITE 失败: %w", err)
		}
		req = authReq
		a.log.Info("INVITE AKA 挑战", "round", round+1, "status", res.StatusCode)
	}
	return nil, fmt.Errorf("voice: INVITE 认证超过最大轮数")
}

// doInviteSingle 发送单次 INVITE（含 PRACK 闭环，A2-1）。
// 使用 TransactionRequest 拦截 1xx，触发 handleProvisional。
func (a *Agent) doInviteSingle(ctx context.Context, req *sip.Request, call *Call) (*sip.Response, error) {
	tx, err := a.cfg.Client.TransactionRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("voice: 创建 INVITE 事务失败: %w", err)
	}
	defer tx.Terminate()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tx.Done():
			// 事务终止（超时/传输错误）
			if terr := tx.Err(); terr != nil {
				return nil, fmt.Errorf("voice: INVITE 事务失败: %w", terr)
			}
			return nil, fmt.Errorf("voice: INVITE 事务意外终止")
		case res, ok := <-tx.Responses():
			if !ok {
				return nil, fmt.Errorf("voice: 事务响应通道关闭")
			}
			// 1xx：PRACK 处理（A2-1 闭环）
			if res.StatusCode >= 100 && res.StatusCode < 200 {
				if perr := a.handleProvisional(ctx, call, req, res); perr != nil {
					a.log.Warn("PRACK 处理失败", "err", perr)
				}
				// 更新 early 状态：直接改 call.State（已在 Actor goroutine 内，
				// 不可调 a.transition，否则死锁）。
				switch res.StatusCode {
				case 180:
					if CanTransition(call.State, StateRinging) {
						call.State = StateRinging
					}
				case 183:
					if CanTransition(call.State, StateEarlyMedia) {
						call.State = StateEarlyMedia
					}
				}
				continue // 继续等最终响应
			}
			// 最终响应
			return res, nil
		}
	}
}

// buildAuthInvite 根据 401/407 构造带 Authorization 的 INVITE。
// CSeq 递增（RFC 3261：同 dialog 内新请求 CSeq 必须递增）。
func (a *Agent) buildAuthInvite(req *sip.Request, res *sip.Response) (*sip.Request, error) {
	chal, isProxy, err := auth.ParseChallenge(res)
	if err != nil {
		return nil, err
	}
	// 用 sim.ComputeDigest 计算响应（与 REGISTER 同一套逻辑）
	params := sim.DigestParams{
		Method:   "INVITE",
		URI:      fmt.Sprintf("sip:%s", req.Recipient.Host),
		Username: a.cfg.IMPI,
	}
	digestRes, err := sim.ComputeDigest(a.cfg.AKAProvider, chal, params)
	if err != nil {
		return nil, fmt.Errorf("AKA 计算失败: %w", err)
	}
	// 克隆请求并递增 CSeq
	newReq := req.Clone()
	if cseqH := newReq.GetHeader("CSeq"); cseqH != nil {
		var cseqNum int
		fmt.Sscanf(cseqH.Value(), "%d", &cseqNum)
		newReq.RemoveHeader("CSeq")
		newReq.AppendHeader(sip.NewHeader("CSeq", fmt.Sprintf("%d INVITE", cseqNum+1)))
	}
	newReq.AppendHeader(auth.BuildAuthorizationHeader(digestRes, isProxy))
	return newReq, nil
}
