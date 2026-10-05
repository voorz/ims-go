package transport

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	"github.com/emiago/sipgo"
	sipsdk "github.com/emiago/sipgo/sip"
)

// New 创建发送管线（未连接）。
func New(cfg Config) *Pipeline {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Pipeline{cfg: cfg}
}

// Connect 预拨号到 addr，并将连接注入 layer（R1/R2）。
// layer 通常来自 sipgo.UserAgent.TransportLayer()。
// 连接经 callbackConn 包装（R3）后通过单连接 Listener 注入。
func (p *Pipeline) Connect(ctx context.Context, layer *sipsdk.TransportLayer, addr string) error {
	dialer := p.cfg.Dialer
	if dialer == nil {
		dialer = NetDialer{}
	}
	raw, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("transport: 拨号 %s 失败: %w", addr, err)
	}
	wrapped := &callbackConn{
		Conn: raw,
		onLost: func() {
			p.cfg.Logger.Info("传输连接丢失", "addr", addr)
			if p.cfg.OnConnectionLost != nil {
				p.cfg.OnConnectionLost(addr)
			}
		},
	}
	ln := &singleConnListener{conn: wrapped, addr: raw.RemoteAddr()}
	p.conn = wrapped
	p.addr = addr

	// ServeTCP 在后台 Accept；单连接 Listener 只产生这一个连接。
	go func() {
		if err := layer.ServeTCP(ln); err != nil {
			p.cfg.Logger.Debug("ServeTCP 退出", "error", err)
		}
	}()
	return nil
}

// Addr 返回当前连接的远端地址；未连接时为空。
func (p *Pipeline) Addr() string { return p.addr }

// Close 关闭管线连接。TransportLayer 由 UserAgent 拥有，不在此关闭；
// 如需排空连接池（R5），调用方关闭 UserAgent 即可。
func (p *Pipeline) Close() error {
	if p.conn != nil {
		err := p.conn.Close()
		p.conn = nil
		return err
	}
	return nil
}

// callbackConn 包装 net.Conn，在 EOF/Close 时触发一次 onLost（R3）。
type callbackConn struct {
	net.Conn
	onLost func()
	once   sync.Once
}

func (c *callbackConn) fire() {
	c.once.Do(func() {
		if c.onLost != nil {
			c.onLost()
		}
	})
}

func (c *callbackConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		c.fire()
	}
	return n, err
}

func (c *callbackConn) Close() error {
	c.fire()
	return c.Conn.Close()
}

// singleConnListener 是只产生一个预拨号连接的 net.Listener（R1）。
type singleConnListener struct {
	conn      net.Conn
	addr      net.Addr
	once      sync.Once
	closed    chan struct{}
	closeOnce sync.Once
}

func (l *singleConnListener) ensureClosed() chan struct{} {
	if l.closed == nil {
		l.closed = make(chan struct{})
	}
	return l.closed
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() { conn = l.conn })
	if conn != nil {
		return conn, nil
	}
	// 已产生过：阻塞直到关闭，让 sipgo 的 Serve 循环等待。
	<-l.ensureClosed()
	return nil, net.ErrClosed
}

func (l *singleConnListener) Close() error {
	l.closeOnce.Do(func() { close(l.ensureClosed()) })
	if l.conn != nil {
		return l.conn.Close()
	}
	return nil
}

func (l *singleConnListener) Addr() net.Addr { return l.addr }

// DoRequest 发送请求；非 INVITE 自动加 15s deadline（R4）。
func DoRequest(ctx context.Context, client *sipgo.Client, req *sipsdk.Request) (*sipsdk.Response, error) {
	if req.Method != sipsdk.INVITE {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, NonInviteTimeout)
		defer cancel()
	}
	return client.Do(ctx, req)
}
