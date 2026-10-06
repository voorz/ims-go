package auth

import (
	"fmt"
	"strings"

	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"

	"github.com/voorz/ims-go/internal/sim"
)

// ParseChallenge 解析 401/407 的认证挑战。
// 返回 challenge、是否为代理认证（407）、错误。
func ParseChallenge(res *sip.Response) (chal *digest.Challenge, isProxy bool, err error) {
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
	val := h.Value()
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

// BuildAuthorizationHeader 由 sim.DigestResult 构造 Authorization 头。
// 使用 sipgo 通用头对象，避免 raw string 拼接（红线#1）。
func BuildAuthorizationHeader(result sim.DigestResult, isProxy bool) sip.Header {
	var sb strings.Builder
	name := "Authorization"
	if isProxy {
		name = "Proxy-Authorization"
	}
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
