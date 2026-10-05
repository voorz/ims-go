package register

import (
	"context"
	"fmt"

	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sip/transport"
)

// handleSecAgree 处理 494（RFC 3329）：
// 从 Security-Server 提取服务端安全机制，重发带 Security-Client/Verify 的 REGISTER。
func (r *Registrar) handleSecAgree(ctx context.Context, req *sip.Request, res *sip.Response) (*sip.Response, error) {
	serverSec := res.GetHeader("Security-Server")
	if serverSec == nil {
		return nil, fmt.Errorf("register: 494 缺少 Security-Server")
	}

	authReq := req.Clone()
	// Security-Verify 回显服务端机制（简化：直接回显值）
	authReq.AppendHeader(sip.NewHeader("Security-Verify", serverSec.Value()))
	// Security-Client 声明客户端机制（ipsec-3gpp）
	authReq.AppendHeader(sip.NewHeader("Security-Client", `ipsec-3gpp; alg=hmac-sha-1-96; ealg=aes-cbc; prot=esp; mod=trans; spi-c=1000; spi-s=1001; port-c=5100; port-s=5101`))

	res2, err := transport.DoRequest(ctx, r.cfg.Client, authReq)
	if err != nil {
		return nil, fmt.Errorf("register: sec-agree 重发失败: %w", err)
	}
	return res2, nil
}
