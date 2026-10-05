package voice

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo"
)

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from State
		to   State
		want bool
	}{
		{StateInit, StateCalling, true},
		{StateInit, StateRinging, true}, // 被叫
		{StateInit, StateConnected, false},
		{StateCalling, StateRinging, true},
		{StateCalling, StateEarlyMedia, true},
		{StateCalling, StateConnected, true},
		{StateCalling, StateTerminating, true},
		{StateCalling, StateInit, false},
		{StateRinging, StateConnected, true},
		{StateRinging, StateTerminating, true},
		{StateRinging, StateCalling, false},
		{StateEarlyMedia, StateConnected, true},
		{StateEarlyMedia, StateTerminating, true},
		{StatePreconditionWait, StateConnected, true},
		{StateConnected, StateTerminating, true},
		{StateConnected, StateRinging, false},
		{StateTerminating, StateTerminated, true},
		{StateTerminating, StateConnected, false},
		{StateTerminated, StateInit, false},
		{StateTerminated, StateTerminated, false},
	}
	for _, tt := range tests {
		if got := CanTransition(tt.from, tt.to); got != tt.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v",
				tt.from, tt.to, got, tt.want)
		}
	}
}

func TestStateString(t *testing.T) {
	if StateConnected.String() != "Connected" {
		t.Errorf("StateConnected.String() = %q", StateConnected.String())
	}
	if StateTerminated.String() != "Terminated" {
		t.Errorf("StateTerminated.String() = %q", StateTerminated.String())
	}
}

func TestAgentDial(t *testing.T) {
	// fake server：INVITE → 180 → 200
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
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
					if !strings.HasPrefix(msg, "INVITE") {
						continue
					}
					cseq, via, callID := "", "", ""
					for _, line := range strings.Split(msg, "\r\n") {
						if strings.HasPrefix(line, "CSeq:") {
							cseq = strings.TrimSpace(strings.TrimPrefix(line, "CSeq:"))
						}
						if strings.HasPrefix(line, "Via:") {
							via = strings.TrimSpace(strings.TrimPrefix(line, "Via:"))
						}
						if strings.HasPrefix(line, "Call-ID:") {
							callID = strings.TrimSpace(strings.TrimPrefix(line, "Call-ID:"))
						}
					}
					base := "Via: " + via + "\r\nCSeq: " + cseq + "\r\nCall-ID: " + callID + "\r\n"
					// 180 Ringing
					_, _ = c.Write([]byte("SIP/2.0 180 Ringing\r\n" + base + "Content-Length: 0\r\n\r\n"))
					time.Sleep(50 * time.Millisecond)
					// 200 OK
					_, _ = c.Write([]byte("SIP/2.0 200 OK\r\n" + base + "Content-Type: application/sdp\r\nContent-Length: 0\r\n\r\n"))
				}
			}(conn)
		}
	}()

	ua, _ := sipgo.NewUA()
	defer ua.Close()
	client, _ := sipgo.NewClient(ua)
	defer client.Close()

	var transitions []string
	agent := NewAgent(Config{
		IMPU:      "sip:alice@example.com",
		PCSCFAddr: ln.Addr().String(),
		Client:    client,
		OnStateChange: func(id string, from, to State) {
			transitions = append(transitions, from.String()+"->"+to.String())
		},
	})
	defer agent.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	callID, err := agent.Dial(ctx, "bob@example.com")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if callID == "" {
		t.Error("callID 为空")
	}
	// 验证快照
	calls := agent.Snapshot()
	if len(calls) != 1 {
		t.Fatalf("Snapshot 呼叫数 = %d", len(calls))
	}
	// Dial 收到 180 后应为 Ringing（fake server 先回 180）
	// 实际可能已到 Connected（200 紧随其后）；只要不在 Init 即可
	if calls[0].State == StateInit {
		t.Error("Dial 后状态仍为 Init")
	}
	t.Logf("transitions: %v", transitions)
}
