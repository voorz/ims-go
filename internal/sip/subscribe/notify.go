package subscribe

import (
	"encoding/xml"
	"strings"
	"time"

	"github.com/emiago/sipgo/sip"
)

// handleNotify 是 sipgo Server 的 NOTIFY 处理器。
// 解析后投入背压队列，立即回 200（避免阻塞传输层）。
func (s *Subscriber) handleNotify(req *sip.Request, tx sip.ServerTransaction) {
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

// parseMWI 解析 message-summary 体（简化：检查 Messages-Waiting）。
func parseMWI(body []byte) *MWI {
	mwi := &MWI{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "messages-waiting:") {
			mwi.Waiting = strings.Contains(lower, "yes")
		}
		if strings.HasPrefix(lower, "message-account:") {
			mwi.Account = strings.TrimSpace(line[len("Message-Account:"):])
		}
	}
	return mwi
}
