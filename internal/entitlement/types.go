package entitlement

import (
	"log/slog"
	"net/http"
	"time"
)

// HTTPClient 是可注入的 HTTP 客户端。
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// TraceSink 是诊断追踪。
type TraceSink interface {
	Trace(action string, detail string)
}

// AuthAction 是 TS.43 认证 action。
type AuthAction struct {
	AuthType     string `json:"auth-type"`
	ActionName   string `json:"action-name"`
	SubscriberID string `json:"subscriber-id"`
	RequestID    int    `json:"request-id"`
}

// EntitlementRequest 是 entitlement 查询。
type EntitlementRequest struct {
	AuthAction
	EntitlementName string `json:"entitlement-name,omitempty"`
	Challenge       string `json:"challenge,omitempty"`
}

// EntitlementResponse 是服务端响应。
type EntitlementResponse struct {
	Status      string `json:"status"`
	Challenge   string `json:"challenge,omitempty"`
	Token       string `json:"token,omitempty"`
	WebsheetURL string `json:"websheet-url,omitempty"`
	Entitlement string `json:"entitlement-status,omitempty"`
}

// Client 是 entitlement 客户端。
type Client struct {
	http    HTTPClient
	log     *slog.Logger
	trace   TraceSink
	baseURL string
	timeout time.Duration
}
