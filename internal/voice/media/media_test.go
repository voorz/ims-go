package media

import (
	"net"
	"testing"
	"time"
)

func TestRTPHeaderRoundTrip(t *testing.T) {
	h := RTPHeader{
		Version:     2,
		Marker:      true,
		PayloadType: 0,
		Seq:         1234,
		Timestamp:   567890,
		SSRC:        0xdeadbeef,
	}
	b := MarshalRTPHeader(h)
	got, err := ParseRTPHeader(b)
	if err != nil {
		t.Fatalf("ParseRTPHeader: %v", err)
	}
	if got != h {
		t.Errorf("round trip 失败: %+v != %+v", got, h)
	}
}

func TestRTPRelayForward(t *testing.T) {
	requireLoopbackUDP(t)
	relay, err := NewRTPRelay(Config{LocalAddr: "127.0.0.1:0", PTMap: map[uint8]uint8{0: 8}})
	if err != nil {
		t.Fatalf("NewRTPRelay: %v", err)
	}
	defer relay.Close()

	// 远端：普通 UDP 监听器
	remoteConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer remoteConn.Close()
	relay.SetRemote(remoteConn.LocalAddr().(*net.UDPAddr))
	time.Sleep(50 * time.Millisecond) // 等转发循环启动

	// 向 relay 发一个 RTP 包（PT=0）
	sender, err := net.DialUDP("udp", nil, relay.LocalAddr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer sender.Close()
	hdr := MarshalRTPHeader(RTPHeader{Version: 2, PayloadType: 0, Seq: 1, SSRC: 123})
	pkt := append(hdr, []byte{0x11, 0x22}...)
	if _, err := sender.Write(pkt); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// 远端应收到 PT=8 的包
	remoteConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := remoteConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("远端未收到转发包: %v", err)
	}
	got, err := ParseRTPHeader(buf[:n])
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.PayloadType != 8 {
		t.Errorf("PT = %d，期望 8（映射后）", got.PayloadType)
	}
	if got.Seq != 1 {
		t.Errorf("Seq = %d", got.Seq)
	}
}

func TestDTMF(t *testing.T) {
	e := DTMFEvent{Event: 5, End: true, Volume: 10, Duration: 160}
	b := EncodeDTMF(e)
	got, err := DecodeDTMF(b)
	if err != nil {
		t.Fatalf("DecodeDTMF: %v", err)
	}
	if got != e {
		t.Errorf("round trip 失败: %+v", got)
	}
	if ev, ok := DTMFEventForChar('5'); !ok || ev != 5 {
		t.Error("'5' 映射失败")
	}
	if ev, ok := DTMFEventForChar('#'); !ok || ev != 11 {
		t.Error("'#' 映射失败")
	}
}

func TestSDPParseRewrite(t *testing.T) {
	sdpStr := "v=0\r\no=- 0 0 IN IP4 10.0.0.1\r\ns=-\r\nc=IN IP4 10.0.0.1\r\nt=0 0\r\nm=audio 5004 RTP/AVP 0 101\r\na=rtpmap:0 PCMU/8000\r\n"
	sdp, err := ParseSDP(sdpStr)
	if err != nil {
		t.Fatalf("ParseSDP: %v", err)
	}
	if sdp.Connection != "10.0.0.1" {
		t.Errorf("Connection = %q", sdp.Connection)
	}
	if len(sdp.Media) != 1 || sdp.Media[0].Port != 5004 {
		t.Errorf("Media 解析错误: %+v", sdp.Media)
	}
	sdp.RewriteConnection("192.168.1.1")
	out := sdp.String()
	if !contains(out, "192.168.1.1") {
		t.Error("重写后未包含新地址")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
