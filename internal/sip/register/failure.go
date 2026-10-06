package register

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	sip "github.com/emiago/sipgo/sip"
)

// DecideFailure 对 REGISTER 失败做二维决策。
func DecideFailure(statusCode int, resHeaders map[string]string, variantIdx, variantTotal int, hasMoreRegistrar bool) FailureDecision {
	// 维度二优先：协议级重试（RFC 要求）
	if after := parseRetryAfter(resHeaders); after > 0 {
		return FailureDecision{
			RetryAfter: after,
			Reason:     fmt.Sprintf("Retry-After: 等待 %v 后重试", after),
		}
	}
	// 423 Min-Expires：调整 expires 后重试（vowifi-go）
	if statusCode == 423 {
		if minExpires := parseMinExpires(resHeaders); minExpires > 0 {
			return FailureDecision{
				RetryAfter: 0, // 立即重试，由调用方调整 expires
				Reason:     fmt.Sprintf("423 Min-Expires: 调整为 %d 后重试", minExpires),
			}
		}
	}

	// 维度一：换花样（vowifi-core）
	if statusCode == 403 && variantIdx+1 < variantTotal {
		return FailureDecision{
			TryNextVariant: true,
			Reason:         "403: 换下一个变体",
		}
	}

	// 换 P-CSCF（vowifi-core）
	if isFailoverStatus(statusCode) && hasMoreRegistrar {
		return FailureDecision{
			AdvanceRegistrar: true,
			Reason:           fmt.Sprintf("%d: 换下一个 P-CSCF", statusCode),
		}
	}

	// 临时失败：退避重试（vowifi-go）
	if isTemporaryFailure(statusCode) {
		return FailureDecision{
			RetryAfter: 30 * time.Second, // 基础退避
			Reason:     fmt.Sprintf("%d: 临时失败，退避重试", statusCode),
		}
	}

	return FailureDecision{
		GiveUp: true,
		Reason: fmt.Sprintf("%d: 永久失败，放弃", statusCode),
	}
}

// parseRetryAfter 解析 Retry-After 头（秒或 HTTP 日期）。
func parseRetryAfter(headers map[string]string) time.Duration {
	val, ok := headers["Retry-After"]
	if !ok {
		return 0
	}
	val = strings.TrimSpace(val)
	if secs, err := strconv.Atoi(val); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	// TODO: HTTP 日期格式
	return 0
}

// parseMinExpires 解析 423 响应的 Min-Expires 头。
func parseMinExpires(headers map[string]string) int {
	val, ok := headers["Min-Expires"]
	if !ok {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(val)); err == nil && secs > 0 {
		return secs
	}
	return 0
}

// isFailoverStatus 判断是否触发换 P-CSCF。
func isFailoverStatus(code int) bool {
	switch code {
	case 408, 500, 502, 503, 504, 480:
		return true
	default:
		return false
	}
}

// isTemporaryFailure 判断是否为临时失败（可退避重试）。
func isTemporaryFailure(code int) bool {
	switch code {
	case 408, 500, 502, 503, 504, 480:
		return true
	default:
		return false
	}
}

// parseUseProxy 从 305 响应的 Contact 头提取代理地址。
func parseUseProxy(res *sip.Response) string {
	h := res.GetHeader("Contact")
	if h == nil {
		return ""
	}
	// Contact: <sip:proxy.example.com> 或 <sip:proxy.example.com:5060>
	val := strings.TrimSpace(h.Value())
	val = strings.Trim(val, "<>")
	// 去掉 sip: 前缀和参数
	if idx := strings.Index(val, ";"); idx > 0 {
		val = val[:idx]
	}
	val = strings.TrimPrefix(val, "sip:")
	return val
}
