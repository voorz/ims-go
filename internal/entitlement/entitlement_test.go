package entitlement

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// fakeHTTP 模拟 entitlement server。
type fakeHTTP struct {
	handler func(req *http.Request) *http.Response
}

func (f *fakeHTTP) Do(req *http.Request) (*http.Response, error) {
	return f.handler(req), nil
}

func jsonResponse(v interface{}) *http.Response {
	data, _ := json.Marshal(v)
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(data)
	w.Close()
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(&buf),
		Header:     make(http.Header),
	}
}

func TestCheckVoWiFiSuccess(t *testing.T) {
	fake := &fakeHTTP{
		handler: func(req *http.Request) *http.Response {
			return jsonResponse(EntitlementResponse{
				Status:      "success",
				Entitlement: "active",
			})
		},
	}
	c := NewClient("https://example.com/entitlement", fake, nil, nil)
	ok, err := c.CheckVoWiFi("user@example.com", nil)
	if err != nil {
		t.Fatalf("CheckVoWiFi: %v", err)
	}
	if !ok {
		t.Error("期望 VoWiFi active")
	}
}

func TestCheckVoWiFiChallengeLoop(t *testing.T) {
	rounds := 0
	fake := &fakeHTTP{
		handler: func(req *http.Request) *http.Response {
			rounds++
			if rounds < 3 {
				return jsonResponse(EntitlementResponse{Status: "challenge", Challenge: "abc"})
			}
			return jsonResponse(EntitlementResponse{Status: "success", Entitlement: "active"})
		},
	}
	c := NewClient("https://example.com/entitlement", fake, nil, nil)
	aka := func(rand, autn []byte) ([]byte, []byte, []byte, error) {
		return []byte{1}, []byte{2}, []byte{3}, nil
	}
	ok, err := c.CheckVoWiFi("user@example.com", aka)
	if err != nil {
		t.Fatalf("CheckVoWiFi: %v", err)
	}
	if !ok {
		t.Error("期望成功")
	}
	if rounds != 3 {
		t.Errorf("轮次 = %d，期望 3", rounds)
	}
}

func TestCheckVoWiFiChallengeLimit(t *testing.T) {
	fake := &fakeHTTP{
		handler: func(req *http.Request) *http.Response {
			return jsonResponse(EntitlementResponse{Status: "challenge", Challenge: "abc"})
		},
	}
	c := NewClient("https://example.com/entitlement", fake, nil, nil)
	aka := func(rand, autn []byte) ([]byte, []byte, []byte, error) {
		return []byte{1}, []byte{2}, []byte{3}, nil
	}
	_, err := c.CheckVoWiFi("user@example.com", aka)
	if err == nil {
		t.Error("超限时期望错误")
	}
}

func TestGetE911Websheet(t *testing.T) {
	fake := &fakeHTTP{
		handler: func(req *http.Request) *http.Response {
			return jsonResponse(EntitlementResponse{
				Status:      "success",
				WebsheetURL: "https://e911.example.com/websheet",
			})
		},
	}
	c := NewClient("https://example.com/entitlement", fake, nil, nil)
	url, err := c.GetE911Websheet("user@example.com")
	if err != nil {
		t.Fatalf("GetE911Websheet: %v", err)
	}
	if url != "https://e911.example.com/websheet" {
		t.Errorf("URL = %q", url)
	}
}

func TestGzipJSON(t *testing.T) {
	data, err := gzipJSON(AuthAction{AuthType: "EAP-AKA", ActionName: "test"})
	if err != nil {
		t.Fatalf("gzipJSON: %v", err)
	}
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		t.Error("不是 gzip 格式")
	}
}
