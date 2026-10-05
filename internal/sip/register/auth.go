package register

import (
	"context"
	"fmt"
	"strings"

	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"

	"github.com/voorz/ims-go/internal/sim"
	"github.com/voorz/ims-go/internal/sip/transport"
)

// handleChallenge 处理 401/407：解析挑战 → sim.ComputeDigest → 重发带 Authorization。
func (r *Registrar) handleChallenge(ctx context.Context, req *sip.Request, res *sip.Response) (*sip.Response, error) {
	chal, isProxy, err := parseChallenge(res)
	if err != nil {
		return nil, fmt.Errorf("register: 解析认证挑战失败: %w", err)
	}

	r.mu.Lock()
	r.nonceCnt++
	nc := r.nonceCnt
	if r.cnonce == "" {
		r.cnonce = fmt.Sprintf("%x", nc*0x9e3779b9)
	}
	cnonce := r.cnonce
	r.mu.Unlock()

	params := sim.DigestParams{
		Username: r.cfg.IMPI,
		URI:      "sip:" + r.cfg.HomeDomain,
		Method:   "REGISTER",
		CNonce:   cnonce,
		Count:    nc,
	}
	result, err := sim.ComputeDigest(r.cfg.AKAProvider, chal, params)
	if err != nil {
		return nil, fmt.Errorf("register: AKA 计算失败: %w", err)
	}

	// 克隆请求并附加 Authorization（sipgo 对象，非 raw string）
	authReq := req.Clone()
	authReq.AppendHeader(buildAuthorizationHeader(result, isProxy))

	res2, err := transport.DoRequest(ctx, r.cfg.Client, authReq)
	if err != nil {
		return nil, fmt.Errorf("register: 认证后重发失败: %w", err)
	}
	return res2, nil
}

// parseChallenge 从 401/407 解析 WWW-Authenticate/Proxy-Authenticate。
func parseChallenge(res *sip.Response) (chal *digest.Challenge, isProxy bool, err error) {
	var h sip.Header
	if res.StatusCode == 407 {
		h = res.GetHeader("Proxy-Authenticate")
		isProxy = true
	} else {
		h = res.GetHeader("WWW-Authenticate")
	}
	if h == nil {
		return nil, false, fmt.Errorf("缺少认证头")
	}
	// sipgo 将认证头解析为通用头；从其字符串值解析参数
	val := h.Value()
	// 去掉 "Digest " 前缀
	val = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(val), "Digest"))
	chal = &digest.Challenge{}
	for _, part := range strings.Split(val, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.TrimSpace(kv[0])
		v := strings.Trim(strings.TrimSpace(kv[1]), `"`)
		switch strings.ToLower(k) {
		case "realm":
			chal.Realm = v
		case "nonce":
			chal.Nonce = v
		case "algorithm":
			chal.Algorithm = v
		case "opaque":
			chal.Opaque = v
		case "qop":
			chal.QOP = strings.Split(v, " ")
		}
	}
	if chal.Nonce == "" {
		return nil, false, fmt.Errorf("挑战缺少 nonce")
	}
	return chal, isProxy, nil
}

// buildAuthorizationHeader 由 sim.DigestResult 构造 Authorization/Proxy-Authorization 头。
// 使用 sipgo 的通用头对象，避免 raw string 拼接（红线#1）。
func buildAuthorizationHeader(result sim.DigestResult, isProxy bool) sip.Header {
	var sb strings.Builder
	name := "Authorization"
	if isProxy {
		name = "Proxy-Authorization"
	}
	// 按 RFC 3261 §22 顺序组装字段值；值来自 sim 计算，非手拼协议帧
	fmt.Fprintf(&sb, `Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s", algorithm=%s`,
		result.Username, result.Realm, result.Nonce, result.URI, result.Response, result.Algorithm)
	if result.CNonce != "" {
		fmt.Fprintf(&sb, `, cnonce="%s"`, result.CNonce)
	}
	if result.Opaque != "" {
		fmt.Fprintf(&sb, `, opaque="%s"`, result.Opaque)
	}
	if result.Qop != "" {
		fmt.Fprintf(&sb, `, qop=%s, nc=%08x`, result.Qop, result.NonceCount)
	}
	if result.SyncFailure && len(result.AUTS) > 0 {
		fmt.Fprintf(&sb, `, auts="%x"`, result.AUTS)
	}
	return sip.NewHeader(name, sb.String())
}
