package register

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sim"
)

// fakePCSCF 是一个最小 P-CSCF：
//   - 无 Authorization 的 REGISTER → 401（Digest AKAv1-MD5 挑战）
//   - 带 Authorization 的 REGISTER → 200（带 GRUU 的 Contact）
//   - Expires: 0 → 200（注销）
type fakePCSCF struct {
	t       *testing.T
	ln      net.Listener
	gotAuth chan string
	gotReg  chan int // 收到的 Expires 值
}

func newFakePCSCF(t *testing.T) *fakePCSCF {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	f := &fakePCSCF{t: t, ln: ln, gotAuth: make(chan string, 8), gotReg: make(chan int, 8)}
	t.Cleanup(func() { ln.Close() })
	go f.serve()
	return f
}

func (f *fakePCSCF) addr() string { return f.ln.Addr().String() }

func (f *fakePCSCF) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakePCSCF) handle(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 16384)
	for {
		n, err := conn.Read(buf)
		if err != nil || n == 0 {
			return
		}
		msg := string(buf[:n])
		// 只处理第一个请求（简化：每次读一个完整 REGISTER）
		if !strings.HasPrefix(msg, "REGISTER") {
			continue
		}
		expires := 600
		hasAuth := false
		var authVal string
		cseq, via := "", ""
		for _, line := range strings.Split(msg, "\r\n") {
			lower := strings.ToLower(line)
			if strings.HasPrefix(lower, "expires:") {
				expires = parseExpiresLine(line)
			}
			if strings.HasPrefix(lower, "authorization:") {
				hasAuth = true
				authVal = strings.TrimSpace(line[len("Authorization:"):])
			}
			if strings.HasPrefix(line, "CSeq:") {
				cseq = strings.TrimSpace(strings.TrimPrefix(line, "CSeq:"))
			}
			if strings.HasPrefix(line, "Via:") {
				via = strings.TrimSpace(strings.TrimPrefix(line, "Via:"))
			}
		}
		select {
		case f.gotReg <- expires:
		default:
		}

		var resp string
		if expires == 0 {
			resp = "SIP/2.0 200 OK\r\nVia: " + via + "\r\nCSeq: " + cseq + "\r\nContent-Length: 0\r\n\r\n"
		} else if !hasAuth {
			// 32 字节 nonce（RAND||AUTN），base64 编码
			nonce := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xAA}, 32))
			resp = "SIP/2.0 401 Unauthorized\r\nVia: " + via + "\r\nCSeq: " + cseq + "\r\n" +
				`WWW-Authenticate: Digest realm="ims.example.com", nonce="` + nonce + `", algorithm=AKAv1-MD5, qop="auth"` + "\r\n" +
				"Content-Length: 0\r\n\r\n"
		} else {
			select {
			case f.gotAuth <- authVal:
			default:
			}
			resp = "SIP/2.0 200 OK\r\nVia: " + via + "\r\nCSeq: " + cseq + "\r\n" +
				"Contact: <sip:alice@10.0.0.2>;expires=600;pub-gruu=\"sip:alice@example.com;gr=urn-1\";temp-gruu=\"sip:temp@example.com;gr=urn-2\"\r\n" +
				"Expires: 600\r\nContent-Length: 0\r\n\r\n"
		}
		_, _ = conn.Write([]byte(resp))
	}
}

func parseExpiresLine(line string) int {
	// 解析 "Expires: 600"
	parts := strings.SplitN(line, ":", 2)
	if len(parts) == 2 {
		var v int
		if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &v); err == nil {
			return v
		}
	}
	return 600
}

func newTestRegistrar(t *testing.T, pcscf string) *Registrar {
	t.Helper()
	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatalf("NewUA: %v", err)
	}
	t.Cleanup(func() { ua.Close() })
	client, err := sipgo.NewClient(ua)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	return New(Config{
		IMPU:        "sip:alice@example.com",
		IMPI:        "alice@example.com",
		HomeDomain:  "example.com",
		PCSCFAddrs:  []string{pcscf},
		Contact:     "10.0.0.2",
		Expires:     600,
		AKAProvider: &fakeAKAProvider{},
		Client:      client,
	})
}

// fakeAKAProvider 恒返回固定 RES（e2e 测试聚焦流程，milenage 已在 WS-2 覆盖）。
type fakeAKAProvider struct{}

func (f *fakeAKAProvider) CalculateAKA(rand, autn []byte) (sim.AKAResult, error) {
	return sim.AKAResult{RES: []byte("test-res-12345678")}, nil
}

// 注意：测试需要先建立传输。简化：在测试内直接让 sipgo client 拨号。
// 实际集成时由 transport.Pipeline 注入。这里测试 Registrar 逻辑，
// 传输层用 sipgo 默认（直连 fake P-CSCF）。

func TestRegisterFullFlow(t *testing.T) {
	f := newFakePCSCF(t)
	r := newTestRegistrar(t, f.addr())

	// 关键：让 sipgo client 能直连 fake P-CSCF。
	// sipgo 默认会 DNS 解析；这里 PCSCFAddr 已是 IP:port，SetDestination 会直连。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := r.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if r.State() != StateRegistered {
		t.Fatalf("State = %s，期望 registered", r.State())
	}
	reg := r.Registration()
	if reg == nil {
		t.Fatal("Registration 为 nil")
	}
	// 验证收到了带 Authorization 的请求
	select {
	case auth := <-f.gotAuth:
		if !strings.Contains(auth, "AKAv1-MD5") {
			t.Errorf("Authorization 缺少 AKAv1-MD5: %s", auth)
		}
		if !strings.Contains(auth, `response="`) {
			t.Errorf("Authorization 缺少 response: %s", auth)
		}
	case <-time.After(2 * time.Second):
		t.Error("fake P-CSCF 未收到带 Authorization 的 REGISTER")
	}
	r.stopRefresh()
}

func TestUnregister(t *testing.T) {
	f := newFakePCSCF(t)
	r := newTestRegistrar(t, f.addr())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := r.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r.stopRefresh()

	if err := r.Unregister(ctx); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if r.State() != StateUnregistered {
		t.Fatalf("State = %s，期望 unregistered", r.State())
	}
	// 验证收到了 Expires: 0
	select {
	case e := <-f.gotReg:
		// 第一个是 600（初始），需要等 0
		for e != 0 {
			select {
			case e = <-f.gotReg:
			case <-time.After(2 * time.Second):
				t.Fatal("未收到 Expires: 0 的注销请求")
			}
		}
	case <-time.After(2 * time.Second):
		t.Error("fake P-CSCF 未收到请求")
	}
}

// failingPCSCF 恒返回 503（模拟故障 P-CSCF）。
type failingPCSCF struct {
	t  *testing.T
	ln net.Listener
}

func newFailingPCSCF(t *testing.T) *failingPCSCF {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	f := &failingPCSCF{t: t, ln: ln}
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
					cseq, via := "", ""
					for _, line := range strings.Split(string(buf[:n]), "\r\n") {
						if strings.HasPrefix(line, "CSeq:") {
							cseq = strings.TrimSpace(strings.TrimPrefix(line, "CSeq:"))
						}
						if strings.HasPrefix(line, "Via:") {
							via = strings.TrimSpace(strings.TrimPrefix(line, "Via:"))
						}
					}
					resp := "SIP/2.0 503 Service Unavailable\r\nVia: " + via + "\r\nCSeq: " + cseq + "\r\nContent-Length: 0\r\n\r\n"
					_, _ = c.Write([]byte(resp))
				}
			}(conn)
		}
	}()
	return f
}

func TestPCSCFFailover(t *testing.T) {
	bad := newFailingPCSCF(t)
	good := newFakePCSCF(t)

	ua, _ := sipgo.NewUA()
	t.Cleanup(func() { ua.Close() })
	client, _ := sipgo.NewClient(ua)
	t.Cleanup(func() { client.Close() })

	var decisions []Decision
	r := New(Config{
		IMPU:        "sip:alice@example.com",
		IMPI:        "alice@example.com",
		HomeDomain:  "example.com",
		PCSCFAddrs:  []string{bad.ln.Addr().String(), good.addr()},
		Contact:     "10.0.0.2",
		Expires:     600,
		AKAProvider: &fakeAKAProvider{},
		Client:      client,
		OnDecision:  func(d Decision) { decisions = append(decisions, d) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if r.State() != StateRegistered {
		t.Fatalf("State = %s，期望 registered", r.State())
	}
	r.stopRefresh()

	// 验证决策记录：尝试了 2 个，选中了 good
	if len(decisions) != 1 {
		t.Fatalf("期望 1 条决策记录，实际 %d", len(decisions))
	}
	d := decisions[0]
	if len(d.Tried) != 2 {
		t.Errorf("Tried = %v，期望 2 个", d.Tried)
	}
	if d.Selected != good.addr() {
		t.Errorf("Selected = %q，期望 %q", d.Selected, good.addr())
	}
}

func TestOnRegisteredCapturesSecurityServer(t *testing.T) {
	r := New(Config{Expires: 600})
	// 构造带 Security-Server 的 200 OK
	res := sip.NewResponse(200, "OK")
	res.AppendHeader(sip.NewHeader("Security-Server", "ipsec-3gpp;alg=hmac-md5-32;ealg=aes-cbc;prot=esp;mod=trans"))
	res.AppendHeader(sip.NewHeader("Contact", "<sip:alice@192.168.1.2>;pub-gruu=\"sip:alice@example.com;gr=123\""))

	if err := r.onRegistered(res); err != nil {
		t.Fatalf("onRegistered: %v", err)
	}
	reg := r.Registration()
	if reg == nil {
		t.Fatalf("Registration 应非空")
	}
	if reg.SecurityServer != "ipsec-3gpp;alg=hmac-md5-32;ealg=aes-cbc;prot=esp;mod=trans" {
		t.Fatalf("SecurityServer 未捕获: %q", reg.SecurityServer)
	}
	r.stopRefresh()
}
