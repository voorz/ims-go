package dns

import (
	"net"
	"testing"
	"time"
)

// requireLoopbackUDP 在回环 UDP 不可用时跳过测试。
// 某些沙箱环境会拦截回环 UDP 回包，此时与真实 DNS/UDP 交互的测试无法运行；
// 在正常 CI 环境中回环 UDP 可用，测试会正常执行。
func requireLoopbackUDP(t *testing.T) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("跳过：无法监听回环 UDP: %v", err)
	}
	defer pc.Close()
	addr := pc.LocalAddr().String()
	go func() {
		buf := make([]byte, 8)
		_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, raddr, err := pc.ReadFrom(buf)
		if err != nil || n != 4 {
			return
		}
		_, _ = pc.WriteTo([]byte("pong"), raddr)
	}()
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Skipf("跳过：回环 UDP 不可用: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Skipf("跳过：回环 UDP 不可用: %v", err)
	}
	buf := make([]byte, 8)
	if _, err := conn.Read(buf); err != nil {
		t.Skipf("跳过：回环 UDP 回包被拦截: %v", err)
	}
}
