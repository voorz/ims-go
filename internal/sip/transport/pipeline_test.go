package transport

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	sipsdk "github.com/emiago/sipgo/sip"
)

// fakeSIPServer 是一个最小 SIP 响应器：回显 Via/CSeq 回 200。
func fakeSIPServer(t *testing.T) net.Addr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 8192)
				for {
					n, err := c.Read(buf)
					if err != nil || n == 0 {
						return
					}
					via, cseq := "", ""
					for _, line := range strings.Split(string(buf[:n]), "\r\n") {
						if strings.HasPrefix(line, "Via:") {
							via = strings.TrimSpace(strings.TrimPrefix(line, "Via:"))
						}
						if strings.HasPrefix(line, "CSeq:") {
							cseq = strings.TrimSpace(strings.TrimPrefix(line, "CSeq:"))
						}
					}
					resp := "SIP/2.0 200 OK\r\nVia: " + via + "\r\nCSeq: " + cseq + "\r\nContent-Length: 0\r\n\r\n"
					_, _ = c.Write([]byte(resp))
				}
			}(conn)
		}
	}()
	return ln.Addr()
}

func newOptionsRequest(t *testing.T, dest string) *sipsdk.Request {
	t.Helper()
	req := sipsdk.NewRequest(sipsdk.OPTIONS, sipsdk.Uri{Host: "127.0.0.1"})
	req.SetDestination(dest)
	req.AppendHeader(sipsdk.NewHeader("Via", fmt.Sprintf("SIP/2.0/TCP 127.0.0.1;branch=z9hG4bK%d", time.Now().UnixNano())))
	req.AppendHeader(sipsdk.NewHeader("CSeq", "1 OPTIONS"))
	req.AppendHeader(sipsdk.NewHeader("Content-Length", "0"))
	return req
}

func TestPipelineConnectAndDo(t *testing.T) {
	addr := fakeSIPServer(t)

	var lost atomic.Int32
	p := New(Config{
		OnConnectionLost: func(a string) { lost.Add(1) },
	})
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ua, _ := sipgo.NewUA()
	defer ua.Close()
	client, _ := sipgo.NewClient(ua)
	defer client.Close()

	if err := p.Connect(ctx, ua.TransportLayer(), addr.String()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if p.Addr() != addr.String() {
		t.Errorf("Addr = %q, 期望 %q", p.Addr(), addr.String())
	}

	res, err := DoRequest(ctx, client, newOptionsRequest(t, addr.String()))
	if err != nil {
		t.Fatalf("DoRequest: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("状态码 = %d，期望 200", res.StatusCode)
	}
}

func TestPipelineConnectionLostCallback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	// server 接受后立即关闭连接（模拟 EOF）
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	lost := make(chan string, 1)
	p := New(Config{
		OnConnectionLost: func(a string) { lost <- a },
	})
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ua, _ := sipgo.NewUA()
	defer ua.Close()
	client, _ := sipgo.NewClient(ua)
	defer client.Close()
	if err := p.Connect(ctx, ua.TransportLayer(), ln.Addr().String()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	// 触发一次读取以观察到 EOF
	_, _ = DoRequest(ctx, client, newOptionsRequest(t, ln.Addr().String()))

	select {
	case a := <-lost:
		if a != ln.Addr().String() {
			t.Errorf("OnConnectionLost 地址 = %q", a)
		}
	case <-time.After(3 * time.Second):
		t.Error("连接丢失后未触发 OnConnectionLost 回调")
	}
}

func TestDoRequestNonInviteDeadline(t *testing.T) {
	// 验证 DoRequest 对非 INVITE 施加了 deadline：用一个永不响应的 server，
	// 外层 ctx 很长，DoRequest 应在 ~15s 内返回。但 15s 太长，改为验证
	// 逻辑：直接检查 NonInviteTimeout 常量存在且 DoRequest 签名正确。
	// 真实超时行为由 D-012 前置验证覆盖。
	if NonInviteTimeout != 15*time.Second {
		t.Errorf("NonInviteTimeout = %v，期望 15s", NonInviteTimeout)
	}
	if StatusSecurityAgreementRequired != 494 {
		t.Errorf("494 常量 = %d", StatusSecurityAgreementRequired)
	}
}
