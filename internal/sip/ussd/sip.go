package ussd

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/emiago/sipgo/sip"
)

// randomHex 生成随机 hex 字符串。
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// BuildMultipartBody 构造 INVITE 的 multipart 体（SDP + USSD XML）。
func BuildMultipartBody(ussdXML []byte) []byte {
	var body bytes.Buffer
	fmt.Fprintf(&body, "--%s\r\nContent-Type: application/sdp\r\n\r\n", multipartBoundary)
	body.Write(buildSDP())
	fmt.Fprintf(&body, "\r\n--%s\r\n", multipartBoundary)
	body.WriteString("Content-Type: " + ContentType + "\r\n")
	body.WriteString("Content-Disposition: render;handling=optional\r\n\r\n")
	body.Write(ussdXML)
	fmt.Fprintf(&body, "\r\n--%s--\r\n", multipartBoundary)
	return body.Bytes()
}

// buildSDP 构造 USSI INVITE 的 SDP（音频能力声明，实际不建媒体）。
func buildSDP() []byte {
	// USSD 不需要真实媒体，SDP 仅为满足 IMS 流程的占位。
	// 取 vowifi-go 的做法：m=audio 0（端口 0 表示不提供媒体）。
	return []byte("v=0\r\n" +
		"o=- 0 0 IN IP4 0.0.0.0\r\n" +
		"s=-\r\n" +
		"c=IN IP4 0.0.0.0\r\n" +
		"t=0 0\r\n" +
		"m=audio 0 RTP/AVP 0\r\n")
}

// dialstringURI 构造 USSD 的 Request-URI。
// 格式：sip:<command>;phone-context=<domain>@<domain>;user=dialstring
func dialstringURI(command, domain string) string {
	command = strings.ReplaceAll(strings.TrimSpace(command), "#", "%23")
	return fmt.Sprintf("sip:%s;phone-context=%s@%s;user=dialstring", command, domain, domain)
}

// BuildInitialInvite 构造初始 INVITE（建 dialog）。
func BuildInitialInvite(cfg Config, command, callID, localTag string, cseq uint32, ussdXML []byte) (*sip.Request, error) {
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("ussd: 命令为空")
	}
	if strings.TrimSpace(callID) == "" {
		return nil, fmt.Errorf("ussd: Call-ID 为空")
	}
	if cseq == 0 {
		return nil, fmt.Errorf("ussd: CSeq 为空")
	}
	domain := strings.TrimSpace(cfg.Domain)
	if domain == "" {
		return nil, fmt.Errorf("ussd: Domain 为空")
	}

	recipient := sip.Uri{}
	if err := sip.ParseUri(dialstringURI(command, domain), &recipient); err != nil {
		return nil, fmt.Errorf("ussd: 解析 Request-URI 失败: %w", err)
	}

	req := sip.NewRequest(sip.INVITE, recipient)
	req.SetDestination(cfg.PCSCFAddr)

	req.AppendHeader(sip.NewHeader("Call-ID", callID))
	req.AppendHeader(sip.NewHeader("CSeq", fmt.Sprintf("%d INVITE", cseq)))
	from := &sip.FromHeader{
		Address: sip.Uri{User: cfg.IMPU},
		Params:  sip.HeaderParams{{K: "tag", V: localTag}},
	}
	req.AppendHeader(from)
	to := &sip.ToHeader{Address: recipient}
	req.AppendHeader(to)

	contact := &sip.ContactHeader{Address: sip.Uri{Host: cfg.Contact}}
	req.AppendHeader(contact)

	req.AppendHeader(sip.NewHeader("Content-Type", "multipart/mixed;boundary="+multipartBoundary))
	req.SetBody(BuildMultipartBody(ussdXML))

	return req, nil
}

// BuildInfo 构造 dialog 内 INFO（g.3gpp.ussd）。
func BuildInfo(s *Session, localIMPU string, ussdXML []byte) (*sip.Request, error) {
	if len(ussdXML) == 0 {
		return nil, fmt.Errorf("ussd: INFO body 为空")
	}
	return buildDialogRequest(s, localIMPU, sip.INFO, ussdXML)
}

// BuildBye 构造 dialog 内 BYE。
func BuildBye(s *Session, localIMPU string) (*sip.Request, error) {
	return buildDialogRequest(s, localIMPU, sip.BYE, nil)
}

// buildDialogRequest 构造 dialog 内请求（INFO/BYE）。
func buildDialogRequest(s *Session, localIMPU string, method sip.RequestMethod, body []byte) (*sip.Request, error) {
	if s == nil {
		return nil, fmt.Errorf("ussd: session 为空")
	}
	s.mu.Lock()
	callID := s.callID
	localTag := s.localTag
	remoteTag := s.remoteTag
	remoteTarget := s.remoteTarget
	cseq := s.cseq
	s.cseq++
	s.mu.Unlock()

	recipient := sip.Uri{}
	target := remoteTarget
	if target == "" {
		return nil, fmt.Errorf("ussd: remote target 为空")
	}
	if err := sip.ParseUri(target, &recipient); err != nil {
		return nil, fmt.Errorf("ussd: 解析 remote target 失败: %w", err)
	}

	// From 用本地 IMPU + 本地 tag；To 用远端 URI + 远端 tag（dialog 内）。
	localURI := sip.Uri{}
	if localIMPU != "" {
		_ = sip.ParseUri(localIMPU, &localURI)
	}
	req := sip.NewRequest(method, recipient)

	req.AppendHeader(sip.NewHeader("Call-ID", callID))
	req.AppendHeader(sip.NewHeader("CSeq", fmt.Sprintf("%d %s", cseq, method)))
	from := &sip.FromHeader{
		Address: localURI,
		Params:  sip.HeaderParams{{K: "tag", V: localTag}},
	}
	req.AppendHeader(from)
	to := &sip.ToHeader{
		Address: recipient,
		Params:  sip.HeaderParams{},
	}
	if remoteTag != "" {
		to.Params = sip.HeaderParams{{K: "tag", V: remoteTag}}
	}
	req.AppendHeader(to)

	if method == sip.INFO {
		req.AppendHeader(sip.NewHeader("Info-Package", InfoPackage))
		req.AppendHeader(sip.NewHeader("Content-Disposition", "info-package"))
		req.AppendHeader(sip.NewHeader("Recv-Info", InfoPackage))
		req.AppendHeader(sip.NewHeader("Accept", ContentType))
		req.AppendHeader(sip.NewHeader("Content-Type", ContentType))
		req.SetBody(body)
	} else {
		req.AppendHeader(sip.NewHeader("Content-Length", "0"))
	}

	return req, nil
}
