package ussd

import (
	"strings"
	"testing"
)

func TestEncodeDecodeXML(t *testing.T) {
	body, err := EncodeXML("*100#", "en")
	if err != nil {
		t.Fatalf("EncodeXML: %v", err)
	}
	if !strings.Contains(string(body), "*100#") {
		t.Fatalf("编码后不含命令: %s", body)
	}
	payload, err := DecodeXML(body)
	if err != nil {
		t.Fatalf("DecodeXML: %v", err)
	}
	if payload.USSDString != "*100#" {
		t.Fatalf("解码不匹配: %q", payload.USSDString)
	}
}

func TestLooksLikeMenu(t *testing.T) {
	menu := "请选择：\n1. 余额查询\n2. 流量查询"
	if !LooksLikeMenu(menu) {
		t.Fatalf("应识别为菜单")
	}
	single := "您的余额为 100 元"
	if LooksLikeMenu(single) {
		t.Fatalf("不应识别为菜单")
	}
}

func TestBuildMultipartBody(t *testing.T) {
	ussdXML, _ := EncodeXML("*100#", "en")
	body := BuildMultipartBody(ussdXML)
	s := string(body)
	if !strings.Contains(s, multipartBoundary) {
		t.Fatalf("multipart 体缺 boundary")
	}
	if !strings.Contains(s, "application/sdp") {
		t.Fatalf("multipart 体缺 SDP 部分")
	}
	if !strings.Contains(s, "*100#") {
		t.Fatalf("multipart 体缺 USSD XML")
	}
	// 能解析回来
	extracted := ExtractFromMultipart(body)
	if len(extracted) == 0 {
		t.Fatalf("ExtractFromMultipart 返回空")
	}
	payload, err := DecodeXML(extracted)
	if err != nil {
		t.Fatalf("解析提取的 XML: %v", err)
	}
	if payload.USSDString != "*100#" {
		t.Fatalf("提取的 XML 不匹配: %q", payload.USSDString)
	}
}

func TestBuildInitialInvite(t *testing.T) {
	cfg := Config{
		IMPU:   "sip:user@ims.example.com",
		Domain: "ims.example.com",
	}
	ussdXML, _ := EncodeXML("*100#", "en")
	req, err := BuildInitialInvite(cfg, "*100#", "callid-123", "tag-abc", 1, ussdXML)
	if err != nil {
		t.Fatalf("BuildInitialInvite: %v", err)
	}
	if req.Method != "INVITE" {
		t.Fatalf("方法应为 INVITE: %s", req.Method)
	}
	// Request-URI 应为 dialstring 格式
	uri := req.Recipient.String()
	if !strings.Contains(uri, "phone-context") || !strings.Contains(uri, "user=dialstring") {
		t.Fatalf("Request-URI 格式错误: %s", uri)
	}
	// # 应编码为 %23
	if !strings.Contains(uri, "%23") {
		t.Fatalf("# 未编码: %s", uri)
	}
	// Content-Type 应为 multipart
	ct := ""
	if h := req.GetHeader("Content-Type"); h != nil {
		ct = h.Value()
	}
	if !strings.Contains(ct, "multipart/mixed") {
		t.Fatalf("Content-Type 应为 multipart: %s", ct)
	}
}

func TestParseResultMenu(t *testing.T) {
	menuXML, _ := EncodeXML("1. 余额\n2. 流量", "en")
	result := ParseResult(menuXML, "sess-1")
	if result.Status != 1 {
		t.Fatalf("菜单应返回 Status=1: %d", result.Status)
	}
	if result.SessionID != "sess-1" {
		t.Fatalf("SessionID 不匹配: %s", result.SessionID)
	}
}

func TestParseResultDone(t *testing.T) {
	doneXML, _ := EncodeXML("余额 100 元", "en")
	result := ParseResult(doneXML, "sess-1")
	if result.Status != 0 {
		t.Fatalf("非菜单应返回 Status=0: %d", result.Status)
	}
	if result.Text != "余额 100 元" {
		t.Fatalf("文本不匹配: %q", result.Text)
	}
}

func TestIsContentType(t *testing.T) {
	if !IsContentType("application/vnd.3gpp.ussd+xml") {
		t.Fatalf("应识别标准类型")
	}
	if !IsContentType("application/vnd.3gpp.ussd+xml;charset=utf-8") {
		t.Fatalf("应识别带参数的类型")
	}
	if IsContentType("text/plain") {
		t.Fatalf("不应识别 text/plain")
	}
}
