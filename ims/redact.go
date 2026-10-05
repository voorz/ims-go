package ims

import (
	"regexp"
	"strings"
)

// NewRedactor 创建脱敏器（内置默认规则）。
func NewRedactor(extra ...RedactRule) *Redactor {
	rules := []RedactRule{
		{
			Name:    "sip-authorization",
			Pattern: regexp.MustCompile(`(?i)(Authorization:\s*Digest\s+.*?response=")[^"]+(")`),
			Replace: `${1}***${2}`,
		},
		{
			Name:    "long-digits",
			Pattern: regexp.MustCompile(`\b\d{10,}\b`),
			Replace: "***",
		},
		{
			Name:    "imsi",
			Pattern: regexp.MustCompile(`(?i)(imsi["':\s=]+)\d{10,15}`),
			Replace: `${1}***`,
		},
	}
	rules = append(rules, extra...)
	return &Redactor{rules: rules}
}

// Redact 对文本应用所有脱敏规则。
func (r *Redactor) Redact(s string) string {
	for _, rule := range r.rules {
		s = rule.Pattern.ReplaceAllString(s, rule.Replace)
	}
	return s
}

// RedactHeaders 对 SIP 头值脱敏（保留头名）。
func (r *Redactor) RedactHeaders(headers map[string]string) map[string]string {
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		// Authorization 头整体脱敏
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Proxy-Authorization") {
			out[k] = "***"
			continue
		}
		out[k] = r.Redact(v)
	}
	return out
}

// DefaultRedactor 是默认脱敏器。
var DefaultRedactor = NewRedactor()
