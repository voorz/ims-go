package sms

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo"

	smscodec "github.com/voorz/ims-go/internal/sip/sms/codec"
)

// fakeSIPServer 接受 MESSAGE 回 200。
type fakeSIPServer struct {
	t  *testing.T
	ln net.Listener
}

func newFakeSIPServer(t *testing.T) *fakeSIPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	f := &fakeSIPServer{t: t, ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 16384)
				for {
					n, err := c.Read(buf)
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
					resp := "SIP/2.0 200 OK\r\nVia: " + via + "\r\nCSeq: " + cseq + "\r\nContent-Length: 0\r\n\r\n"
					_, _ = c.Write([]byte(resp))
				}
			}(conn)
		}
	}()
	return f
}

func newTestSMS(t *testing.T, server *fakeSIPServer) *SMS {
	t.Helper()
	ua, _ := sipgo.NewUA()
	t.Cleanup(func() { ua.Close() })
	client, _ := sipgo.NewClient(ua)
	t.Cleanup(func() { client.Close() })
	srv, _ := sipgo.NewServer(ua)
	t.Cleanup(func() { srv.Close() })

	return New(Config{
		IMPU:      "sip:alice@example.com",
		PCSCFAddr: server.ln.Addr().String(),
		Client:    client,
		Server:    srv,
	})
}

func TestSendSMS(t *testing.T) {
	f := newFakeSIPServer(t)
	s := newTestSMS(t, f)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, err := s.Send(ctx, "+1234567890", "Hello, Bob!")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id == "" {
		t.Error("Message ID 为空")
	}

	status, err := s.Status(ctx, id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != StatusSent {
		t.Errorf("Status = %s，期望 sent", status)
	}
}

func TestReassembly(t *testing.T) {
	f := newFakeSIPServer(t)
	s := newTestSMS(t, f)

	// 模拟两片分片
	concat1 := smscodec.ConcatInfo{IsConcat: true, Ref: 1, Total: 2, Seq: 1}
	concat2 := smscodec.ConcatInfo{IsConcat: true, Ref: 1, Total: 2, Seq: 2}

	if _, ok := s.reassemble(concat1, []byte("Hello, "), "alice"); ok {
		t.Error("第一片后不应完成重组")
	}
	full, ok := s.reassemble(concat2, []byte("Bob!"), "alice")
	if !ok {
		t.Fatal("第二片后应完成重组")
	}
	if string(full) != "Hello, Bob!" {
		t.Errorf("重组结果 = %q", string(full))
	}
}
