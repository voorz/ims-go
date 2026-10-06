// Package keepalive 提供 SIP 保活（WS-10）。
//
// 覆盖：OPTIONS ping、TCP CRLF pong、失败阈值→触发恢复（联动 WS-5）。
// 类型定义见 types.go（门禁③）。
package keepalive

import (
	"context"
	"log/slog"
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
