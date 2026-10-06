package register

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FailureDecision 是二维失败决策的结果。
//
// 维度一（换花样，vowifi-core）：403/配置码 → 换下一个变体
// 维度二（延迟重试，vowifi-go）：Retry-After/423 → 等待后重试
// 两个维度正交，可组合。
type FailureDecision struct {
	// TryNextVariant 换下一个变体
	TryNextVariant bool
	// RetryAfter 等待后重试同一变体（0 表示不等待）
	RetryAfter time.Duration
	// AdvanceRegistrar 换下一个 P-CSCF
	AdvanceRegistrar bool
	// GiveUp 放弃
	GiveUp bool
	// Reason 决策原因（用于日志和 P2 记录）
	Reason string
}

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
