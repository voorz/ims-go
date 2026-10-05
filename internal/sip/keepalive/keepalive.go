// Package keepalive 提供 SIP 保活（WS-10）。
//
// 覆盖：OPTIONS ping、TCP CRLF pong、失败阈值→触发恢复（联动 WS-5）。
package keepalive

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/emiago/sipgo/sip"
)

// Config 是保活配置。
type Config struct {
	// Target 是保活目标（P-CSCF 地址）。
	Target string
	// Interval 是保活间隔；0 用默认 30s。
	Interval time.Duration
	// MaxFailures 是连续失败阈值；0 用默认 3。
	MaxFailures int
	// OnFailed 是达到阈值时的回调（触发恢复，联动 WS-5）。
	OnFailed func()
	// Sender 发送 OPTIONS（由调用方注入，便于测试）。
	Sender func(ctx context.Context, req *sip.Request) (*sip.Response, error)
	// Logger 为空时用 slog 默认。
	Logger *slog.Logger
}

// Keeper 执行保活。
type Keeper struct {
	cfg    Config
	log    *slog.Logger
	mu     sync.Mutex
	fails  int
	cancel context.CancelFunc
}

// New 创建保活器。
func New(cfg Config) *Keeper {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.MaxFailures <= 0 {
		cfg.MaxFailures = 3
	}
	return &Keeper{cfg: cfg, log: cfg.Logger}
}

// Start 启动保活循环。
func (k *Keeper) Start() {
	k.mu.Lock()
	if k.cancel != nil {
		k.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	k.cancel = cancel
	k.mu.Unlock()

	go func() {
		ticker := time.NewTicker(k.cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := k.ping(ctx); err != nil {
					k.mu.Lock()
					k.fails++
					fails := k.fails
					k.mu.Unlock()
					k.log.Warn("保活失败", "fails", fails, "error", err)
					if fails >= k.cfg.MaxFailures {
						k.log.Error("保活连续失败达阈值，触发恢复")
						if k.cfg.OnFailed != nil {
							k.cfg.OnFailed()
						}
						k.mu.Lock()
						k.fails = 0
						k.mu.Unlock()
					}
				} else {
					k.mu.Lock()
					k.fails = 0
					k.mu.Unlock()
				}
			}
		}
	}()
}

// Stop 停止保活。
func (k *Keeper) Stop() {
	k.mu.Lock()
	if k.cancel != nil {
		k.cancel()
		k.cancel = nil
	}
	k.mu.Unlock()
}

// ping 发送一次 OPTIONS 保活。
func (k *Keeper) ping(ctx context.Context) error {
	recipient := sip.Uri{Host: k.cfg.Target}
	recipient.UriParams = sip.HeaderParams{{K: "transport", V: "tcp"}}
	req := sip.NewRequest(sip.OPTIONS, recipient)
	req.SetDestination(k.cfg.Target)
	req.AppendHeader(sip.NewHeader("Content-Length", "0"))

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if k.cfg.Sender != nil {
		_, err := k.cfg.Sender(pingCtx, req)
		return err
	}
	return fmt.Errorf("keepalive: 未配置 Sender")
}

// SendCRLFPong 发送 TCP CRLF pong（保活）。
func SendCRLFPong(conn net.Conn) error {
	_, err := conn.Write([]byte("\r\n\r\n"))
	return err
}
