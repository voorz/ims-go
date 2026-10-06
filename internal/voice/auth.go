package voice

import (
	"context"
	"fmt"

	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sim"
	"github.com/voorz/ims-go/internal/sip/auth"
	"github.com/voorz/ims-go/internal/sip/transport"
)

// doInviteWithAuth 发送 INVITE 并处理 401/407 Digest-AKA 挑战。
// 复用 sim 包的 AKA 计算（与 REGISTER 同一套，D-015）。
// 最多 3 轮挑战（防认证循环）。
func (a *Agent) doInviteWithAuth(ctx context.Context, req *sip.Request) (*sip.Response, error) {
	const maxRounds = 3
	for round := 0; round < maxRounds; round++ {
		res, err := transport.DoRequest(ctx, a.cfg.Client, req)
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
