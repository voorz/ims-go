package register

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"

	"github.com/voorz/ims-go/internal/sim"
	"github.com/voorz/ims-go/internal/sip/transport"
)

// nonceFingerprint 计算 nonce 的指纹（SHA256 前 8 字节 hex）。
// 用于检测重放的 AKA 挑战：要求 fresh challenge 时 nonce 不能重复。
func nonceFingerprint(nonce string) string {
	trimmed := strings.TrimSpace(nonce)
	if trimmed == "" {
		return "missing"
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:8])
}

// maxChallengeRounds 是 AKA 挑战最大轮数（vowifi-core/vowifi-go 均为 3）。
// 超过则判定为认证循环，放弃。
const maxChallengeRounds = 3

// handleChallenge 处理 401/407：解析挑战 → sim.ComputeDigest → 重发带 Authorization。
// 包含 nonce 指纹防重放（vowifi-core 生产做法）。
func (r *Registrar) handleChallenge(ctx context.Context, req *sip.Request, res *sip.Response) (*sip.Response, error) {
	return r.handleChallengeWithHistory(ctx, req, res, "", 0)
}

// handleChallengeWithHistory 带 nonce 历史和轮数做防重放/防循环检查。
func (r *Registrar) handleChallengeWithHistory(ctx context.Context, req *sip.Request, res *sip.Response, prevFingerprint string, round int) (*sip.Response, error) {
	if round >= maxChallengeRounds {
		return nil, fmt.Errorf("register: AKA 挑战超过最大轮数 %d，疑似认证循环", maxChallengeRounds)
	}
	chal, isProxy, err := parseChallenge(res)
	if err != nil {
		return nil, fmt.Errorf("register: 解析认证挑战失败: %w", err)
	}

	// Nonce 指纹防重放：要求 fresh challenge 时，nonce 不能与上次相同
	fp := nonceFingerprint(chal.Nonce)
	if prevFingerprint != "" && fp == prevFingerprint {
		return nil, fmt.Errorf("register: AKA 挑战重放攻击 (fingerprint=%s)", fp)
	}
	r.log.Info("AKA 挑战", "round", round+1, "fingerprint", fp)

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
	// 保存 protected refresh 所需状态（从带认证的请求提取）
	if res2.StatusCode == 200 {
		r.saveRefreshState(authReq, res2)
	}
	// 多轮挑战：如果又是 401/407，递归处理（带轮数限制和指纹）
	if res2.StatusCode == 401 || res2.StatusCode == 407 {
		r.log.Info("收到多轮 AKA 挑战", "round", round+2)
		return r.handleChallengeWithHistory(ctx, authReq, res2, fp, round+1)
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
