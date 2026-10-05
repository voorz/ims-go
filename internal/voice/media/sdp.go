package media

import (
	"fmt"
	"strconv"
	"strings"
)

// SDP 是简化的 SDP 对象模型。
type SDP struct {
	Connection string // c= 行的地址
	Media      []SDPMedia
}

// SDPMedia 是一路媒体。
type SDPMedia struct {
	Type    string // "audio"
	Port    int
	Proto   string   // "RTP/AVP"
	Formats []string // PT 列表
	Attrs   map[string]string
}

// ParseSDP 解析 SDP（简化，只取 c= 和 m=）。
func ParseSDP(s string) (*SDP, error) {
	sdp := &SDP{}
	var cur *SDPMedia
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if len(line) < 2 || line[1] != '=' {
			continue
		}
		typ, val := line[0], line[2:]
		switch typ {
		case 'c':
			// c=IN IP4 10.0.0.1
			parts := strings.Fields(val)
			if len(parts) >= 3 {
				sdp.Connection = parts[2]
			}
		case 'm':
			// m=audio 5004 RTP/AVP 0 101
			parts := strings.Fields(val)
			if len(parts) >= 4 {
				port, _ := strconv.Atoi(parts[1])
				m := SDPMedia{
					Type:    parts[0],
					Port:    port,
					Proto:   parts[2],
					Formats: parts[3:],
					Attrs:   make(map[string]string),
				}
				sdp.Media = append(sdp.Media, m)
				cur = &sdp.Media[len(sdp.Media)-1]
			}
		case 'a':
			if cur != nil {
				// a=rtpmap:0 PCMU/8000
				if idx := strings.Index(val, ":"); idx >= 0 {
					cur.Attrs[val[:idx]] = val[idx+1:]
				} else {
					cur.Attrs[val] = ""
				}
			}
		}
	}
	if len(sdp.Media) == 0 {
		return nil, fmt.Errorf("media: SDP 无媒体行")
	}
	return sdp, nil
}

// RewriteConnection 重写 SDP 的连接地址（NAT/隧道场景）。
func (s *SDP) RewriteConnection(newAddr string) {
	s.Connection = newAddr
}

// String 序列化 SDP。
func (s *SDP) String() string {
	var sb strings.Builder
	sb.WriteString("v=0\r\n")
	sb.WriteString("o=- 0 0 IN IP4 " + s.Connection + "\r\n")
	sb.WriteString("s=-\r\n")
	sb.WriteString("c=IN IP4 " + s.Connection + "\r\n")
	sb.WriteString("t=0 0\r\n")
	for _, m := range s.Media {
		sb.WriteString(fmt.Sprintf("m=%s %d %s %s\r\n",
			m.Type, m.Port, m.Proto, strings.Join(m.Formats, " ")))
		for k, v := range m.Attrs {
			if v != "" {
				sb.WriteString("a=" + k + ":" + v + "\r\n")
			} else {
				sb.WriteString("a=" + k + "\r\n")
			}
		}
	}
	return sb.String()
}
