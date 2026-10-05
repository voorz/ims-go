// Package sms 实现 SMS over IMS（WS-8）。
//
// 覆盖：MO 发送/队列/重试、分片发送、投递状态机；
// MT 接收/去重/分片重组、确认；二进制短信分类（H6）。
// PDU 编解码在子包 codec（收拢 vowifi-go internal/smscodec）。
package sms

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/emiago/sipgo"
)

// DeliveryStatus 是投递状态（强类型）。
type DeliveryStatus int

const (
	StatusQueued DeliveryStatus = iota
	StatusSending
	StatusSent
	StatusDelivered
	StatusFailed
)

func (s DeliveryStatus) String() string {
	switch s {
	case StatusQueued:
		return "queued"
	case StatusSending:
		return "sending"
	case StatusSent:
		return "sent"
	case StatusDelivered:
		return "delivered"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Message 是一条短信。
type Message struct {
	ID     string
	From   string
	To     string
	Text   string
	At     time.Time
	Concat ConcatRef // 分片引用（长短信）
}

// ConcatRef 标识长短信分片。
type ConcatRef struct {
	Ref   uint16
	Total uint8
	Seq   uint8
}

// DeliveryRecord 是一条投递记录。
type DeliveryRecord struct {
	MessageID string
	To        string
	Status    DeliveryStatus
	Attempts  int
	At        time.Time
	Error     string
}

// DeliveryStore 是投递记录存储接口（消费方实现持久化）。
type DeliveryStore interface {
	Save(ctx context.Context, rec DeliveryRecord) error
	Get(ctx context.Context, messageID string) (DeliveryRecord, error)
	UpdateStatus(ctx context.Context, messageID string, status DeliveryStatus, errMsg string) error
}

// Config 是 SMS 模块配置。
type Config struct {
	// IMPU 是本地标识。
	IMPU string
	// PCSCFAddr 是 P-CSCF 地址。
	PCSCFAddr string
	// Client 是 sipgo 客户端（发送）。
	Client *sipgo.Client
	// Server 是 sipgo 服务端（接收 MESSAGE）。
	Server *sipgo.Server
	// Store 是投递存储；nil 时用内存实现。
	Store DeliveryStore
	// OnMessage 是收到 MT 短信的回调。
	OnMessage func(m Message)
	// MaxRetries 是 MO 重试次数；0 用默认 3。
	MaxRetries int
	// Logger 为空时用 slog 默认。
	Logger *slog.Logger
}

// SMS 是短信模块。
type SMS struct {
	cfg Config
	log *slog.Logger

	mu       sync.RWMutex
	store    DeliveryStore
	seen     map[string]time.Time // MT 去重
	reassemb map[uint16]*reassembly
}

type reassembly struct {
	total uint8
	parts map[uint8][]byte
	from  string
	at    time.Time
}
