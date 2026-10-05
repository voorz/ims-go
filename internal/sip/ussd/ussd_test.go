package ussd

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo"
)

// fakeUSSDServer 模拟 USSD 网关：
//   - 收到 *100# → 返回菜单
//   - 收到 "1" → 返回余额并结束
type fakeUSSDServer struct {
	t  *testing.T
	ln net.Listener
}

func newFakeUSSDServer(t *testing.T) *fakeUSSDServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	f := &fakeUSSDServer{t: t, ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(conn)
		}
	}()
	return f
}

func (f *fakeUSSDServer) handle(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 16384)
	for {
		n, err := conn.Read(buf)
		if err != nil || n == 0 {
			return
		}
		msg := string(buf[:n])
		if !strings.HasPrefix(msg, "MESSAGE") {
			continue
		}
		cseq, via := "", ""
		for _, line := range strings.Split(msg, "\r\n") {
			if strings.HasPrefix(line, "CSeq:") {
				cseq = strings.TrimSpace(strings.TrimPrefix(line, "CSeq:"))
			}
			if strings.HasPrefix(line, "Via:") {
				via = strings.TrimSpace(strings.TrimPrefix(line, "Via:"))
			}
		}
		// 提取 USSD 字符串
		var ussdStr string
		if idx := strings.Index(msg, "<ussd-string>"); idx >= 0 {
			end := strings.Index(msg[idx:], "</ussd-string>")
			if end >= 0 {
				ussdStr = msg[idx+len("<ussd-string>") : idx+end]
			}
		}
		var reply string
		switch ussdStr {
		case "*100#":
			reply = "1. 查余额\n2. 充值"
		case "1":
			reply = "余额：100元"
		default:
			reply = "未知指令"
		}
		body, _ := EncodeXML(reply, "en")
		resp := "SIP/2.0 200 OK\r\nVia: " + via + "\r\nCSeq: " + cseq + "\r\n" +
			"Content-Type: " + ContentType + "\r\n" +
			"Content-Length: " + itoa(len(body)) + "\r\n\r\n" + string(body)
		_, _ = conn.Write([]byte(resp))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestUSSDSession(t *testing.T) {
	f := newFakeUSSDServer(t)

	ua, _ := sipgo.NewUA()
	defer ua.Close()
	client, _ := sipgo.NewClient(ua)
	defer client.Close()

	s := NewSession(Config{
		IMPU:       "sip:alice@example.com",
		USSDTarget: "127.0.0.1",
		PCSCFAddr:  f.ln.Addr().String(),
		Client:     client,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 发起
	reply, err := s.SendUSSD(ctx, "*100#")
	if err != nil {
		t.Fatalf("SendUSSD: %v", err)
	}
	if !strings.Contains(reply, "查余额") {
		t.Errorf("回复 = %q，期望菜单", reply)
	}
	if s.State() != StateActive {
		t.Errorf("State = %s，期望 active", s.State())
	}

	// 继续
	reply, err = s.ContinueUSSD(ctx, "1")
	if err != nil {
		t.Fatalf("ContinueUSSD: %v", err)
	}
	if !strings.Contains(reply, "余额") {
		t.Errorf("回复 = %q，期望余额", reply)
	}

	// 取消
	if err := s.CancelUSSD(ctx); err != nil {
		t.Fatalf("CancelUSSD: %v", err)
	}
	if s.State() != StateClosed {
		t.Errorf("State = %s，期望 closed", s.State())
	}
}

func TestEncodeDecodeXML(t *testing.T) {
	body, err := EncodeXML("*100#", "en")
	if err != nil {
		t.Fatalf("EncodeXML: %v", err)
	}
	p, err := DecodeXML(body)
	if err != nil {
		t.Fatalf("DecodeXML: %v", err)
	}
	if p.USSDString != "*100#" {
		t.Errorf("USSDString = %q", p.USSDString)
	}
	if p.Language != "en" {
		t.Errorf("Language = %q", p.Language)
	}
}

func TestSessionTimeout(t *testing.T) {
	f := newFakeUSSDServer(t)
	ua, _ := sipgo.NewUA()
	defer ua.Close()
	client, _ := sipgo.NewClient(ua)
	defer client.Close()

	s := NewSession(Config{
		IMPU:           "sip:alice@example.com",
		USSDTarget:     "127.0.0.1",
		PCSCFAddr:      f.ln.Addr().String(),
		Client:         client,
		SessionTimeout: 100 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.SendUSSD(ctx, "*100#"); err != nil {
		t.Fatalf("SendUSSD: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if s.State() != StateClosed {
		t.Errorf("超时后 State = %s，期望 closed", s.State())
	}
}
