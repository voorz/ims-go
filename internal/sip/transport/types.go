// Package transport 提供基于上游 sipgo 的 SIP 传输层（D-012）。
//
// 设计（对应计划 WS-6 R1–R6）：
//   - 预先拨号：通过 Dialer（隧道网络）拨号，不依赖 sipgo 内部拨号。
//   - 单连接注入：预拨号的连接经 singleConnListener 注入 TransportLayer.ServeTCP。
//   - 连接复用：sipgo connectionReuse（R1/R2）；Close 时排空连接池（R5）。
//   - 故障回调：callbackConn 在 EOF/Close 时触发 OnConnectionLost（R3，对接 WS-5 恢复）。
//   - 非 INVITE 15s deadline（R4）；494 常量（R6）。
package transport

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"
)

// StatusSecurityAgreementRequired 是 RFC 3329 sec-agree 的 494 响应码（R6）。
const StatusSecurityAgreementRequired = 494

// NonInviteTimeout 是非 INVITE 请求的默认 deadline（R4）。
const NonInviteTimeout = 15 * time.Second

// Dialer 通过底层网络（隧道内）拨号。
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Config 是传输层配置。
type Config struct {
	// Dialer 用于预先拨号；nil 时使用 net.Dialer（直连，仅测试）。
	Dialer Dialer
	// OnConnectionLost 在连接 EOF/关闭时调用（R3，触发上层恢复）。
	OnConnectionLost func(addr string)
	// Logger 为空时用 slog 默认。
	Logger *slog.Logger
}

// Pipeline 是 SIP 发送管线：预拨号 → 单连接注入 → sipgo 传输层。
// TransportLayer 由 sipgo.UserAgent 拥有，Pipeline 只负责连接的
// 预拨号、包装与注入（Connect 时传入）。
type Pipeline struct {
	cfg  Config
	conn net.Conn
	addr string
}

// NetDialer 用标准库拨号（直连，测试用）。
type NetDialer struct {
	Dialer net.Dialer
}

func (d NetDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.Dialer.DialContext(ctx, network, address)
}

// callbackConn 包装 net.Conn，在 EOF/Close 时触发一次 onLost（R3）。
type callbackConn struct {
	net.Conn
	onLost func()
	once   sync.Once
}

// singleConnListener 是只产生一个预拨号连接的 net.Listener（R1）。
type singleConnListener struct {
	conn      net.Conn
	addr      net.Addr
	once      sync.Once
	closed    chan struct{}
	closeOnce sync.Once
}
