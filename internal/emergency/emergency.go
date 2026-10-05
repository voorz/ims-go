// Package emergency 提供紧急呼叫策略（WS-15）。
//
// 覆盖：号码/URN 识别、IR.51 紧急 ePDG FQDN、默认禁用（三处一致）。
// 紧急呼叫默认禁用，需显式 opt-in。
package emergency

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// ServiceURN 是 RFC 5031 顶级紧急服务 URN。
	ServiceURN = "urn:service:sos"
	// AnonymousIMPU 是无 IMPU 时的匿名标识。
	AnonymousIMPU = "sip:anonymous@anonymous.invalid"
)

// ErrOriginatingDisabled 在检测到紧急目的地但未启用时返回。
var ErrOriginatingDisabled = errors.New("emergency originating is disabled")

var emergencyNumbers = map[string]struct{}{
	"112": {}, "999": {}, "911": {}, "000": {},
	"110": {}, "118": {}, "119": {},
}

// IsEmergencyDestination 报告是否为紧急 URN 或号码。
func IsEmergencyDestination(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if isEmergencyURN(value) {
		return true
	}
	digits := emergencyDigits(value)
	_, ok := emergencyNumbers[digits]
	return ok
}

// ServiceURNFor 将拨打的紧急号码/URN 映射为 Request-URI URN。
func ServiceURNFor(value string) string {
	value = strings.TrimSpace(value)
	if isEmergencyURN(value) {
		return strings.ToLower(value)
	}
	if IsEmergencyDestination(value) {
		return ServiceURN
	}
	return ""
}

// EmergencyEPDGAddr 是 IR.51 紧急 ePDG FQDN。
func EmergencyEPDGAddr(mcc, mnc string) string {
	return fmt.Sprintf("sos.epdg.epc.mnc%s.mcc%s.pub.3gppnetwork.org", plmn3(mnc), plmn3(mcc))
}

// plmn3 将 PLMN 补齐为 3 位。
func plmn3(s string) string {
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}

func isEmergencyURN(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return lower == ServiceURN || strings.HasPrefix(lower, ServiceURN+".")
}

func emergencyDigits(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(strings.ToLower(value), "tel:")
	value = strings.TrimPrefix(value, "sip:")
	if at := strings.IndexByte(value, '@'); at >= 0 {
		value = value[:at]
	}
	var digits strings.Builder
	for _, c := range value {
		if c >= '0' && c <= '9' {
			digits.WriteRune(c)
		}
	}
	return digits.String()
}

// Policy 是紧急呼叫策略（默认禁用）。
type Policy struct {
	// Enabled 显式 opt-in 后为 true。
	Enabled bool
}

// Check 检查目的地；禁用时返回 ErrOriginatingDisabled。
func (p Policy) Check(destination string) error {
	if IsEmergencyDestination(destination) && !p.Enabled {
		return ErrOriginatingDisabled
	}
	return nil
}
