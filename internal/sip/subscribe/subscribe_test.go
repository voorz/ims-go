package subscribe

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo"
)

// fakeServer 接受 SUBSCRIBE 回 200，然后主动发 NOTIFY。
type fakeServer struct {
	t    *testing.T
	ln   net.Listener
	conn net.Conn
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	f := &fakeServer{t: t, ln: ln}
	t.Cleanup(func() { ln.Close() })
	go f.serve()
	return f
}

func (f *fakeServer) addr() string { return f.ln.Addr().String() }

func (f *fakeServer) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.conn = conn
		go f.handle(conn)
	}
}

func (f *fakeServer) handle(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 16384)
	for {
		n, err := conn.Read(buf)
		if err != nil || n == 0 {
			return
		}
		msg := string(buf[:n])
		if !strings.HasPrefix(msg, "SUBSCRIBE") {
			continue
		}
		callID, cseq, via, from := "", "", "", ""
		for _, line := range strings.Split(msg, "\r\n") {
			if strings.HasPrefix(line, "Call-ID:") {
				callID = strings.TrimSpace(strings.TrimPrefix(line, "Call-ID:"))
			}
			if strings.HasPrefix(line, "CSeq:") {
				cseq = strings.TrimSpace(strings.TrimPrefix(line, "CSeq:"))
			}
			if strings.HasPrefix(line, "Via:") {
				via = strings.TrimSpace(strings.TrimPrefix(line, "Via:"))
			}
			if strings.HasPrefix(line, "From:") {
				from = strings.TrimSpace(strings.TrimPrefix(line, "From:"))
			}
		}
		// 回 200（带 To tag 建立 dialog）
		toTag := "server-tag-123"
		resp := "SIP/2.0 200 OK\r\nVia: " + via + "\r\nCSeq: " + cseq + "\r\n" +
			"Call-ID: " + callID + "\r\n" +
			"From: " + from + "\r\n" +
			"To: <sip:alice@example.com>;tag=" + toTag + "\r\n" +
			"Expires: 3600\r\nContent-Length: 0\r\n\r\n"
		_, _ = conn.Write([]byte(resp))

		// 延迟后发 NOTIFY（reginfo）
		time.Sleep(100 * time.Millisecond)
		reginfo := `<?xml version="1.0"?>
<reginfo xmlns="urn:ietf:params:xml:ns:reginfo" version="1" state="full">
  <registration aor="sip:alice@example.com" id="1" state="active">
    <contact id="1" state="active" event="registered" uri="sip:alice@10.0.0.2"/>
  </registration>
</reginfo>`
		notify := "NOTIFY sip:alice@10.0.0.2 SIP/2.0\r\n" +
			"Via: SIP/2.0/TCP 127.0.0.1;branch=z9hG4bKnotify1\r\n" +
			"Call-ID: " + callID + "\r\n" +
			"CSeq: 1 NOTIFY\r\n" +
			"From: <sip:alice@example.com>;tag=" + toTag + "\r\n" +
			"To: <sip:alice@example.com>;tag=" + extractTag(from) + "\r\n" +
			"Event: reg\r\n" +
			"Subscription-State: active;expires=3599\r\n" +
			"Content-Type: application/reginfo+xml\r\n" +
			"Content-Length: " + strconv.Itoa(len(reginfo)) + "\r\n\r\n" + reginfo
		_, _ = conn.Write([]byte(notify))
	}
}

func extractTag(from string) string {
	if i := strings.Index(from, "tag="); i >= 0 {
		tag := from[i+4:]
		if j := strings.Index(tag, ";"); j >= 0 {
			tag = tag[:j]
		}
		return strings.TrimSpace(tag)
	}
	return ""
}

func TestSubscribeNotifyFlow(t *testing.T) {
	f := newFakeServer(t)

	ua, _ := sipgo.NewUA()
	defer ua.Close()
	client, _ := sipgo.NewClient(ua)
	defer client.Close()
	server, _ := sipgo.NewServer(ua)
	defer server.Close()

	notifies := make(chan NotifyEvent, 4)
	sub := New(Config{
		IMPU:      "alice@example.com",
		Event:     "reg",
		Expires:   3600,
		PCSCFAddr: f.addr(),
		Contact:   "10.0.0.2",
		Client:    client,
		Server:    server,
		OnNotify:  func(n NotifyEvent) { notifies <- n },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sub.Subscribe(ctx); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if sub.State() != StateActive {
		t.Fatalf("State = %s，期望 active", sub.State())
	}
	sub.stopRefresh()

	// 等 NOTIFY
	select {
	case ne := <-notifies:
		if ne.Event != "reg" {
			t.Errorf("Event = %q，期望 reg", ne.Event)
		}
		if ne.RegInfo == nil {
			t.Fatal("RegInfo 为 nil")
		}
		if len(ne.RegInfo.Contacts) != 1 {
			t.Fatalf("Contacts 数量 = %d，期望 1", len(ne.RegInfo.Contacts))
		}
		c := ne.RegInfo.Contacts[0]
		if c.URI != "sip:alice@10.0.0.2" {
			t.Errorf("Contact URI = %q", c.URI)
		}
		if c.State != "active" || c.Event != "registered" {
			t.Errorf("Contact state/event = %s/%s", c.State, c.Event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("超时未收到 NOTIFY")
	}
}

func TestParseRegInfo(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
<reginfo xmlns="urn:ietf:params:xml:ns:reginfo" version="42" state="full">
  <registration aor="sip:bob@example.com" id="1" state="active">
    <contact id="1" state="active" event="registered" uri="sip:bob@10.0.0.3"/>
    <contact id="2" state="terminated" event="deactivated" uri="sip:bob@10.0.0.4"/>
  </registration>
</reginfo>`)
	ri, err := parseRegInfo(body)
	if err != nil {
		t.Fatalf("parseRegInfo: %v", err)
	}
	if ri.Version != "42" {
		t.Errorf("Version = %q", ri.Version)
	}
	if len(ri.Contacts) != 2 {
		t.Fatalf("Contacts = %d", len(ri.Contacts))
	}
	if ri.Contacts[1].State != "terminated" {
		t.Errorf("第二个 contact state = %q", ri.Contacts[1].State)
	}
}

func TestParseMWI(t *testing.T) {
	body := []byte("Messages-Waiting: yes\r\nMessage-Account: sip:alice@example.com\r\n")
	mwi := parseMWI(body)
	if !mwi.Waiting {
		t.Error("Waiting 应为 true")
	}
	if mwi.Account != "sip:alice@example.com" {
		t.Errorf("Account = %q", mwi.Account)
	}
}

func TestSecurityVerifyInheritance(t *testing.T) {
	ua, _ := sipgo.NewUA()
	client, _ := sipgo.NewClient(ua)
	server, _ := sipgo.NewServer(ua)
	defer ua.Close()

	s := New(Config{
		IMPU:      "sip:alice@example.com",
		Event:     "reg",
		PCSCFAddr: "127.0.0.1:5060",
		Contact:   "sip:alice@192.168.1.2",
		Client:    client,
		Server:    server,
	})
	// 初始无 Security-Verify
	req := s.buildSubscribe(3600)
	if h := req.GetHeader("Security-Verify"); h != nil {
		t.Fatalf("初始不应有 Security-Verify")
	}
	// 继承后应有
	s.SetSecurityVerify("ipsec-3gpp;alg=hmac-md5-32;ealg=aes-cbc")
	req = s.buildSubscribe(3600)
	h := req.GetHeader("Security-Verify")
	if h == nil {
		t.Fatalf("继承后应有 Security-Verify")
	}
	if h.Value() != "ipsec-3gpp;alg=hmac-md5-32;ealg=aes-cbc" {
		t.Fatalf("Security-Verify 值不匹配: %q", h.Value())
	}
}
