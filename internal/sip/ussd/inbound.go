package ussd

import (
	"strings"

	"github.com/emiago/sipgo/sip"
)

// handleInboundInfo 处理入站 INFO（网络侧 USSD 推送）。
func (s *Service) handleInboundInfo(req *sip.Request, tx sip.ServerTransaction) {
	if !s.matchAndDeliver(req, "INFO") {
		res := sip.NewResponseFromRequest(req, 481, "Subscription Does Not Exist", nil)
		_ = tx.Respond(res)
		return
	}
	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	_ = tx.Respond(res)
}

// handleInboundBye 处理入站 BYE（网络侧终止会话）。
func (s *Service) handleInboundBye(req *sip.Request, tx sip.ServerTransaction) {
	session := s.matchSession(req)
	if session == nil {
		res := sip.NewResponseFromRequest(req, 481, "Subscription Does Not Exist", nil)
		_ = tx.Respond(res)
		return
	}
	// BYE 可能带最终 USSD
	result := InfoResult{Err: nil}
	if len(req.Body()) > 0 {
		xmlBody := ExtractFromMultipart(req.Body())
		if len(xmlBody) == 0 {
			xmlBody = req.Body()
		}
		if payload, err := DecodeXML(xmlBody); err == nil {
			result.Text = payload.USSDString
			result.RawXML = string(xmlBody)
		}
	}
	s.deliverResult(session, result)
	s.closeSession(session)

	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	_ = tx.Respond(res)
}

// matchSession 按 Call-ID 匹配活动会话。
func (s *Service) matchSession(req *sip.Request) *Session {
	session := s.activeSession()
	if session == nil {
		return nil
	}
	var callID string
	if h := req.GetHeader("Call-ID"); h != nil {
		callID = strings.TrimSpace(h.Value())
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if callID == "" || callID != session.callID {
		return nil
	}
	return session
}

// matchAndDeliver 匹配会话并投递 INFO 结果。
func (s *Service) matchAndDeliver(req *sip.Request, what string) bool {
	if req.Method != sip.INFO {
		return false
	}
	var ct string
	if h := req.GetHeader("Content-Type"); h != nil {
		ct = h.Value()
	}
	if !IsContentType(ct) {
		s.log.Warn("入站 USSD Content-Type 不匹配", "ct", ct)
		return false
	}
	session := s.matchSession(req)
	if session == nil {
		return false
	}
	xmlBody := ExtractFromMultipart(req.Body())
	if len(xmlBody) == 0 {
		xmlBody = req.Body()
	}
	payload, err := DecodeXML(xmlBody)
	result := InfoResult{RawXML: string(xmlBody)}
	if err != nil {
		result.Err = err
	} else {
		result.Text = payload.USSDString
	}
	s.deliverResult(session, result)
	session.touch(s.cfg.SessionTimeout, s.log)
	return true
}

// deliverResult 向等待的操作投递结果（非阻塞）。
func (s *Service) deliverResult(session *Session, result InfoResult) {
	session.mu.Lock()
	ch := session.resultCh
	session.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- result:
	default:
	}
}
