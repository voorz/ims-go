package ussd

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sip/transport"
)

// XMLPayload 是 USSD XML 信封（对象化，非 string 拼）。
type XMLPayload struct {
	XMLName    xml.Name `xml:"ussd-data"`
	Xmlns      string   `xml:"xmlns,attr"`
	Language   string   `xml:"language"`
	USSDString string   `xml:"ussd-string"`
}

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

// NewSession 创建 USSD 会话。
func NewSession(cfg Config) *Session {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.SessionTimeout <= 0 {
		cfg.SessionTimeout = 5 * time.Minute
	}
	return &Session{
		cfg:       cfg,
		log:       cfg.Logger,
		state:     StateIdle,
		sessionID: fmt.Sprintf("ussd-%d", time.Now().UnixNano()),
	}
}

// State 返回会话状态。
func (s *Session) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// SendUSSD 发起 USSD 会话。
func (s *Session) SendUSSD(ctx context.Context, ussdString string) (string, error) {
	s.mu.Lock()
	if s.state != StateIdle {
		s.mu.Unlock()
		return "", fmt.Errorf("ussd: 会话状态 %s，无法发起", s.state)
	}
	s.state = StateActive
	s.lastAt = time.Now()
	s.mu.Unlock()

	s.resetTimer()

	body, err := EncodeXML(ussdString, "en")
	if err != nil {
		return "", err
	}
	res, err := s.sendMessage(ctx, body)
	if err != nil {
		s.setState(StateClosed)
		return "", err
	}
	return s.parseResponse(res)
}

// ContinueUSSD 在会话中继续。
func (s *Session) ContinueUSSD(ctx context.Context, input string) (string, error) {
	s.mu.RLock()
	if s.state != StateActive {
		s.mu.RUnlock()
		return "", fmt.Errorf("ussd: 会话未激活")
	}
	s.mu.RUnlock()

	body, err := EncodeXML(input, "en")
	if err != nil {
		return "", err
	}
	res, err := s.sendMessage(ctx, body)
	if err != nil {
		return "", err
	}
	s.touch()
	return s.parseResponse(res)
}

// CancelUSSD 取消会话。
func (s *Session) CancelUSSD(ctx context.Context) error {
	s.stopTimer()
	s.setState(StateClosed)
	// 发送空 USSD 表示结束（简化）
	_, _ = s.sendMessage(ctx, nil)
	return nil
}

func (s *Session) setState(to State) {
	s.mu.Lock()
	s.state = to
	s.mu.Unlock()
}

func (s *Session) touch() {
	s.mu.Lock()
	s.lastAt = time.Now()
	s.mu.Unlock()
	s.resetTimer()
}

func (s *Session) resetTimer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(s.cfg.SessionTimeout, func() {
		s.log.Info("USSD 会话超时", "id", s.sessionID)
		s.setState(StateClosed)
	})
}

func (s *Session) stopTimer() {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()
}

// sendMessage 发送 SIP MESSAGE（USSD XML 体）。
func (s *Session) sendMessage(ctx context.Context, body []byte) (*sip.Response, error) {
	recipient := sip.Uri{Host: s.cfg.USSDTarget}
	recipient.UriParams = sip.HeaderParams{{K: "transport", V: "tcp"}}
	req := sip.NewRequest(sip.MESSAGE, recipient)
	req.SetDestination(s.cfg.PCSCFAddr)
	if body != nil {
		req.AppendHeader(sip.NewHeader("Content-Type", ContentType))
		req.SetBody(body)
	} else {
		req.AppendHeader(sip.NewHeader("Content-Length", "0"))
	}
	return transport.DoRequest(ctx, s.cfg.Client, req)
}

// parseResponse 解析 USSD 响应（200 OK 的 XML 体）。
func (s *Session) parseResponse(res *sip.Response) (string, error) {
	if res.StatusCode != 200 {
		return "", fmt.Errorf("ussd: 失败，状态码 %d", res.StatusCode)
	}
	body := res.Body()
	if len(body) == 0 {
		return "", nil
	}
	payload, err := DecodeXML(body)
	if err != nil {
		// 非 XML 响应，直接返回文本
		return string(body), nil
	}
	return payload.USSDString, nil
}
