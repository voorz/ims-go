package ussd

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"strings"
)

// EncodeXML 编码 USSD 字符串为 XML。
func EncodeXML(text, language string) ([]byte, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("ussd: 命令为空")
	}
	if language == "" {
		language = "en"
	}
	payload := XMLPayload{Xmlns: ContentType, Language: language, USSDString: text}
	body, err := xml.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("ussd: XML 编码失败: %w", err)
	}
	return append([]byte(xml.Header), body...), nil
}

// DecodeXML 解码 USSD XML。
func DecodeXML(body []byte) (*XMLPayload, error) {
	if len(body) == 0 {
		return nil, errors.New("ussd: XML body 为空")
	}
	var payload XMLPayload
	if err := xml.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("ussd: XML 解析失败: %w", err)
	}
	return &payload, nil
}

// IsContentType 判断是否为 USSD XML 媒体类型。
func IsContentType(contentType string) bool {
	value := strings.ToLower(strings.TrimSpace(contentType))
	return strings.Contains(value, ContentType) ||
		strings.Contains(value, "application/3gpp-ussd+xml")
}

// LooksLikeMenu 判断文本是否为 USSD 菜单（vowifi-go：至少两个编号选项）。
func LooksLikeMenu(message string) bool {
	choices := 0
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 2 || line[0] < '1' || line[0] > '9' {
			continue
		}
		switch line[1] {
		case '.', ')', ':', ' ':
			choices++
		}
	}
	return choices > 1
}

// ExtractFromMultipart 从 multipart 体提取 USSD XML 部分。
func ExtractFromMultipart(body []byte) []byte {
	boundary := multipartBoundary
	if first, _, found := bytes.Cut(body, []byte("\n")); found {
		line := strings.TrimSpace(string(first))
		if strings.HasPrefix(line, "--") {
			boundary = strings.TrimPrefix(line, "--")
		}
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := reader.NextPart()
		if err != nil {
			return nil
		}
		partBody, err := io.ReadAll(part)
		if err != nil {
			return nil
		}
		mediaType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if IsContentType(mediaType) {
			return bytes.TrimSpace(partBody)
		}
	}
}

// ParseResult 将 SIP body 映射为 USSD 结果。
func ParseResult(body []byte, sessionID string) *Result {
	result := &Result{}
	if len(body) == 0 {
		result.Text = "(空响应)"
		return result
	}
	result.RawXML = string(body)
	xmlBody := ExtractFromMultipart(body)
	if len(xmlBody) == 0 {
		xmlBody = body
	}
	payload, err := DecodeXML(xmlBody)
	if err != nil {
		result.Text = string(body)
		return result
	}
	result.Text = payload.USSDString
	if LooksLikeMenu(result.Text) {
		result.Status = 1
		result.SessionID = sessionID
	}
	return result
}
