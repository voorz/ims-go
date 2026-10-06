package subscribe

import (
	"context"
	"encoding/xml"
	"strings"
	"time"

	"github.com/emiago/sipgo/sip"
)

// handleNotify 是 sipgo Server 的 NOTIFY 处理器。
// 解析后投入背压队列，立即回 200（避免阻塞传输层）。
//
// P0 修复：验证 NOTIFY 属于我们的订阅 dialog（Call-ID 匹配），
// 不匹配的回 481（RFC 3265 §3.2）。
func (s *Subscriber) handleNotify(req *sip.Request, tx sip.ServerTransaction) {
	// Dialog 验证：Call-ID 必须匹配我们的订阅
	s.mu.RLock()
	ourCallID := s.callID
	s.mu.RUnlock()
	if ourCallID != "" {
		if h := req.GetHeader("Call-ID"); h == nil || strings.TrimSpace(h.Value()) != ourCallID {
			s.log.Warn("NOTIFY Call-ID 不匹配，拒绝",
				"expected", ourCallID,
				"got", headerValue(req, "Call-ID"))
			res := sip.NewResponseFromRequest(req, 481, "Subscription Does Not Exist", nil)
			_ = tx.Respond(res)
			return
		}
	}

	event := ""
	if h := req.GetHeader("Event"); h != nil {
		event = strings.TrimSpace(h.Value())
	}

	ne := NotifyEvent{
		At:    time.Now(),
		Event: event,
	}

	// 取消息体
	if body := req.Body(); len(body) > 0 {
		ne.Raw = append([]byte(nil), body...)
		switch {
		case strings.HasPrefix(event, "reg"):
			if ri, err := parseRegInfo(body); err == nil {
				ne.RegInfo = ri
			} else {
				s.log.Warn("reginfo 解析失败", "error", err)
			}
		case strings.Contains(event, "message-summary"):
			ne.MWI = parseMWI(body)
		}
	}

	// 背压队列：满时丢弃最旧（NOTIFY 是状态同步，新者覆盖旧者）
	select {
	case s.notifyQ <- ne:
	default:
		select {
		case <-s.notifyQ:
			s.log.Debug("NOTIFY 队列满，丢弃最旧")
		default:
		}
		select {
		case s.notifyQ <- ne:
		default:
			s.log.Warn("NOTIFY 队列满，丢弃当前")
		}
	}

	// 立即回 200
	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	_ = tx.Respond(res)

	// 订阅被终止（非自己主动 unsubscribed），自动重订（vowifi-go 做法）
	if parseSubscriptionStateHeader(req.GetHeader("Subscription-State")) == "terminated" && s.State() == StateActive {
		s.log.Info("NOTIFY 指示订阅终止，自动重订")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := s.Resubscribe(ctx); err != nil {
				s.log.Error("重订失败", "error", err)
			}
		}()
	}
}

// headerValue 安全取头值。
func headerValue(req *sip.Request, name string) string {
	if h := req.GetHeader(name); h != nil {
		return strings.TrimSpace(h.Value())
	}
	return ""
}

// dispatchLoop 从队列取 NOTIFY 并回调（串行，避免并发回调）。
func (s *Subscriber) dispatchLoop() {
	for ne := range s.notifyQ {
		if s.cfg.OnNotify != nil {
			s.cfg.OnNotify(ne)
		}
	}
}

// reginfoXML 对应 RFC 3680 的 reginfo 文档（简化子集）。
type reginfoXML struct {
	XMLName xml.Name `xml:"reginfo"`
	Version string   `xml:"version,attr"`
	Regs    []regXML `xml:"registration"`
}

type regXML struct {
	AOR      string       `xml:"aor,attr"`
	State    string       `xml:"state,attr"`
	Contacts []contactXML `xml:"contact"`
}

type contactXML struct {
	URI   string `xml:"uri,attr"`
	State string `xml:"state,attr"`
	Event string `xml:"event,attr"`
}

// parseRegInfo 解析 reginfo XML。
func parseRegInfo(body []byte) (*RegInfo, error) {
	var doc reginfoXML
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	ri := &RegInfo{Version: doc.Version}
	for _, reg := range doc.Regs {
		for _, c := range reg.Contacts {
			ri.Contacts = append(ri.Contacts, RegContact{
				URI:   c.URI,
				State: c.State,
				Event: c.Event,
			})
		}
	}
	return ri, nil
}

// parseMWI 解析 message-summary 体。
// vowifi-go 做法：Messages-Waiting 认 yes/1/true；解析 Voice-Message new/old 计数。
func parseMWI(body []byte) *MWI {
	mwi := &MWI{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "messages-waiting:") {
			val := strings.TrimSpace(line[len("Messages-Waiting:"):])
			valLower := strings.ToLower(val)
			// vowifi-go: 认 yes/1/true
			mwi.Waiting = valLower == "yes" || valLower == "1" || valLower == "true"
		}
		if strings.HasPrefix(lower, "message-account:") {
			mwi.Account = strings.TrimSpace(line[len("Message-Account:"):])
		}
		// Voice-Message: 2/0 (new/old)
		if strings.HasPrefix(lower, "voice-message:") {
			val := strings.TrimSpace(line[len("Voice-Message:"):])
			parts := strings.Split(val, "/")
			if len(parts) == 2 {
				// 格式：new/old
				mwi.VoiceNew = parseCount(parts[0])
				mwi.VoiceOld = parseCount(parts[1])
			}
		}
	}
	return mwi
}

// parseCount 解析计数字符串，失败返回 0。
func parseCount(s string) int {
	s = strings.TrimSpace(s)
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
