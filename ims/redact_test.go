package ims

import (
	"regexp"
	"testing"
)

func TestRedactAuthorization(t *testing.T) {
	r := NewRedactor()
	in := `Authorization: Digest username="user", response="abc123def456", nonce="xyz"`
	got := r.Redact(in)
	if contains(got, "abc123def456") {
		t.Errorf("response 未脱敏: %q", got)
	}
	if !contains(got, "***") {
		t.Errorf("期望包含 ***: %q", got)
	}
}

func TestRedactLongDigits(t *testing.T) {
	r := NewRedactor()
	got := r.Redact("IMSI 460001234567890 active")
	if contains(got, "460001234567890") {
		t.Errorf("长数字未脱敏: %q", got)
	}
}

func TestRedactCustomRule(t *testing.T) {
	r := NewRedactor(RedactRule{
		Name:    "custom",
		Pattern: regexp.MustCompile(`secret-\w+`),
		Replace: "[REDACTED]",
	})
	got := r.Redact("token=secret-abc123")
	if got != "token=[REDACTED]" {
		t.Errorf("自定义规则未生效: %q", got)
	}
}

func TestRedactHeaders(t *testing.T) {
	r := NewRedactor()
	headers := map[string]string{
		"Authorization": `Digest response="secret"`,
		"From":          "sip:user@example.com",
	}
	got := r.RedactHeaders(headers)
	if got["Authorization"] != "***" {
		t.Errorf("Authorization 未脱敏: %q", got["Authorization"])
	}
	if got["From"] != "sip:user@example.com" {
		t.Errorf("From 不应被修改: %q", got["From"])
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
