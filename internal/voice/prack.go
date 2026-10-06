package voice

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sip/dialog"
	"github.com/voorz/ims-go/internal/sip/transport"
)

// PRACK（RFC 3262）：可靠临时响应的确认。
//
// 流程：
//  1. INVITE 声明 Supported: 100rel
//  2. 收到 1xx 若带 Require: 100rel → 提取 RSeq → 发 PRACK
//  3. PRACK 用 early dialog（Call-ID + From tag + 1xx 的 To tag）
//  4. sipgo 事务层负责 PRACK 重传（D-012，不自建退避）

// handleProvisional 处理 1xx 临时响应：PRACK + early dialog 学习。
// 返回是否需要继续等待最终响应。
func (a *Agent) handleProvisional(ctx context.Context, call *Call, req *sip.Request, res *sip.Response) error {
	// 提取 RSeq
	rseq := parseRSeq(res)
	if rseq <= 0 {
		return nil // 非可靠临时响应，无需 PRACK
	}
	// 检查是否要求 100rel
	if !requires100rel(res) {
		return nil
	}
	// 构造 early dialog（To tag 来自 1xx）
	localTag, _ := req.From().Params.Get("tag")
	dlg := &dialog.Dialog{
		ID: dialog.ID{
			CallID:   req.CallID().Value(),
			LocalTag: localTag,
		},
	}
	// 从 1xx 学 To tag（early dialog）
	if h := res.GetHeader("To"); h != nil {
		if to, ok := h.(*sip.ToHeader); ok {
			for _, p := range to.Params {
				if p.K == "tag" {
					dlg.ID.RemoteTag = p.V
					break
				}
			}
		}
	}
	// 发 PRACK
	prack := dlg.NewPRACK(rseq)
	prack.SetDestination(a.cfg.PCSCFAddr)
	if _, err := transport.DoRequest(ctx, a.cfg.Client, prack); err != nil {
		return fmt.Errorf("voice: PRACK 失败: %w", err)
	}
	a.log.Info("PRACK 已发送", "callID", call.ID, "rseq", rseq)
	return nil
}

// parseRSeq 从 1xx 提取 RSeq 头。
func parseRSeq(res *sip.Response) int {
	h := res.GetHeader("RSeq")
	if h == nil {
		return 0
	}
	v, _ := strconv.Atoi(strings.TrimSpace(h.Value()))
	return v
}

// requires100rel 检查 1xx 是否要求 100rel。
func requires100rel(res *sip.Response) bool {
	h := res.GetHeader("Require")
	if h == nil {
		return false
	}
	return strings.Contains(strings.ToLower(h.Value()), "100rel")
}
