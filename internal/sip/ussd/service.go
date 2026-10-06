package ussd

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sip/transport"
)

// NewService 创建 USSD 服务并注册入站 INFO/BYE 处理器。
func NewService(cfg Config) *Service {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.SessionTimeout <= 0 {
		cfg.SessionTimeout = 5 * time.Minute
	}
	s := &Service{cfg: cfg, log: cfg.Logger}
	if cfg.Server != nil {
		cfg.Server.OnRequest(sip.INFO, s.handleInboundInfo)
		cfg.Server.OnRequest(sip.BYE, s.handleInboundBye)
	}
	return s
}

// activeSession 返回活动会话（无则 nil）。
func (s *Service) activeSession() *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil || !s.session.IsActive() {
		return nil
	}
	return s.session
}

// Send 发起 USSD（INVITE 建 dialog，等待网络结果）。
func (s *Service) Send(ctx context.Context, ussdString string) (*Result, error) {
	ussdString = strings.TrimSpace(ussdString)
	if ussdString == "" {
		return nil, fmt.Errorf("ussd: 命令为空")
	}
	if s.activeSession() != nil {
		return nil, fmt.Errorf("ussd: 已有活动会话，请先继续或取消")
	}

	body, err := EncodeXML(ussdString, "en")
	if err != nil {
		return nil, err
	}

	session := &Session{
		id:        "ussd-" + randomHex(8),
		callID:    randomHex(32),
		localTag:  "tag-" + randomHex(8),
		cseq:      1,
		state:     StateActive,
		resultCh:  make(chan InfoResult, 4),
		createdAt: time.Now(),
		lastAt:    time.Now(),
	}
	session.resetTimer(s.cfg.SessionTimeout, s.log)

	req, err := BuildInitialInvite(s.cfg, ussdString, session.callID, session.localTag, session.cseq, body)
	if err != nil {
		return nil, err
	}
	session.cseq++

	s.mu.Lock()
	s.session = session
	s.mu.Unlock()

	txCtx, cancel := context.WithTimeout(ctx, transactionTimeout)
	defer cancel()
	res, err := transport.DoRequest(txCtx, s.cfg.Client, req)
	if err != nil {
		s.closeSession(session)
		return &Result{Text: err.Error(), Status: 2}, err
	}

	// 从 200 OK 学习 dialog（To tag、Contact）
	s.learnDialog(session, res)

	result := ParseResult(res.Body(), session.id)
	if result.Status != 1 {
		// 非菜单：会话结束
		s.closeSession(session)
	} else {
		session.touch(s.cfg.SessionTimeout, s.log)
	}
	return result, nil
}

// Continue 在活动会话中继续（INFO）。
func (s *Service) Continue(ctx context.Context, input string) (*Result, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, fmt.Errorf("ussd: 输入为空")
	}
	session := s.activeSession()
	if session == nil {
		return nil, fmt.Errorf("ussd: 无活动会话")
	}

	body, err := EncodeXML(input, "en")
	if err != nil {
		return nil, err
	}

	req, err := BuildInfo(session, s.cfg.IMPU, body)
	if err != nil {
		return nil, err
	}

	txCtx, cancel := context.WithTimeout(ctx, transactionTimeout)
	defer cancel()
	res, err := transport.DoRequest(txCtx, s.cfg.Client, req)
	if err != nil {
		s.closeSession(session)
		return &Result{Text: err.Error(), Status: 2}, err
	}

	// 200 可能直接带 USSD，否则等入站 INFO
	result := s.parseOrWaitResult(txCtx, res, session)
	if result.Status != 1 {
		s.closeSession(session)
	} else {
		session.touch(s.cfg.SessionTimeout, s.log)
	}
	return result, nil
}

// Cancel 取消会话（BYE）。
func (s *Service) Cancel(ctx context.Context) error {
	session := s.activeSession()
	if session == nil {
		return nil
	}

	req, err := BuildBye(session, s.cfg.IMPU)
	if err != nil {
		s.closeSession(session)
		return err
	}

	txCtx, cancel := context.WithTimeout(ctx, transactionTimeout)
	defer cancel()
	_, _ = transport.DoRequest(txCtx, s.cfg.Client, req)
	s.closeSession(session)
	return nil
}

// ActiveSessionID 返回活动会话 ID（无则 ""）。
func (s *Service) ActiveSessionID() string {
	if session := s.activeSession(); session != nil {
		return session.ID()
	}
	return ""
}

// learnDialog 从 INVITE 200 OK 学习 dialog 信息。
func (s *Service) learnDialog(session *Session, res *sip.Response) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if to := res.To(); to != nil {
		for _, p := range to.Params {
			if strings.EqualFold(p.K, "tag") {
				session.remoteTag = p.V
			}
		}
	}
	if contact := res.Contact(); contact != nil {
		session.remoteTarget = contact.Address.String()
	}
	session.lastAt = time.Now()
}

// parseOrWaitResult：200 直接带 USSD 则解析，否则等入站 INFO。
func (s *Service) parseOrWaitResult(ctx context.Context, res *sip.Response, session *Session) *Result {
	if len(res.Body()) > 0 && (len(ExtractFromMultipart(res.Body())) > 0 || IsContentType(headerValue(res, "Content-Type"))) {
		return ParseResult(res.Body(), session.ID())
	}
	// 等入站 INFO
	select {
	case info := <-session.resultCh:
		if info.Err != nil {
			return &Result{Text: info.Err.Error(), Status: 2, RawXML: info.RawXML}
		}
		result := &Result{Text: info.Text, RawXML: info.RawXML}
		if LooksLikeMenu(info.Text) {
			result.Status = 1
			result.SessionID = session.ID()
		}
		return result
	case <-ctx.Done():
		return &Result{Text: "USSD 响应超时", Status: 5, SessionID: session.ID()}
	}
}

// closeSession 关闭会话。
func (s *Service) closeSession(session *Session) {
	if session == nil {
		return
	}
	s.mu.Lock()
	if s.session == session {
		s.session = nil
	}
	s.mu.Unlock()
	session.mu.Lock()
	session.state = StateClosed
	if session.timer != nil {
		session.timer.Stop()
		session.timer = nil
	}
	session.mu.Unlock()
}

// headerValue 取响应头值。
func headerValue(res *sip.Response, name string) string {
	if h := res.GetHeader(name); h != nil {
		return h.Value()
	}
	return ""
}

// resetTimer 重置会话超时定时器。
func (s *Session) resetTimer(timeout time.Duration, log *slog.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
	}
	id := s.id
	s.timer = time.AfterFunc(timeout, func() {
		log.Info("USSD 会话超时", "id", id)
		s.mu.Lock()
		s.state = StateClosed
		s.mu.Unlock()
	})
}

// touch 更新活动时间并重置超时。
func (s *Session) touch(timeout time.Duration, log *slog.Logger) {
	s.mu.Lock()
	s.lastAt = time.Now()
	s.mu.Unlock()
	s.resetTimer(timeout, log)
}
