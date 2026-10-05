package subscribe

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sip/transport"
)

// New 创建订阅器并注册 NOTIFY 处理器。
func New(cfg Config) *Subscriber {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Expires <= 0 {
		cfg.Expires = 3600
	}
	if cfg.NotifyQueueSize <= 0 {
		cfg.NotifyQueueSize = 16
	}
	s := &Subscriber{
		cfg:     cfg,
		log:     cfg.Logger,
		state:   StateIdle,
		notifyQ: make(chan NotifyEvent, cfg.NotifyQueueSize),
	}
	// 注册 NOTIFY 处理器
	cfg.Server.OnRequest(sip.NOTIFY, s.handleNotify)
	// 启动分发循环
	go s.dispatchLoop()
	return s
}

// State 返回当前订阅状态。
func (s *Subscriber) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Subscriber) setState(to State) {
	s.mu.Lock()
	from := s.state
	s.state = to
	s.mu.Unlock()
	if from != to {
		s.log.Info("订阅状态变更", "from", from.String(), "to", to.String())
		if s.cfg.OnStateChange != nil {
			s.cfg.OnStateChange(from, to)
		}
	}
}

// Subscribe 发起 SUBSCRIBE（独立 Call-ID，继承 Security-Verify）。
func (s *Subscriber) Subscribe(ctx context.Context) error {
	s.setState(StateSubscribing)

	s.mu.Lock()
	s.callID = fmt.Sprintf("%d-subscribe", time.Now().UnixNano())
	s.localTag = fmt.Sprintf("tag-%d", time.Now().UnixNano()%1000000)
	s.cseq = 1
	s.mu.Unlock()

	req := s.buildSubscribe(s.cfg.Expires)
	res, err := transport.DoRequest(ctx, s.cfg.Client, req)
	if err != nil {
		s.setState(StateFailed)
		return fmt.Errorf("subscribe: SUBSCRIBE 失败: %w", err)
	}
	if res.StatusCode != 200 {
		s.setState(StateFailed)
		return fmt.Errorf("subscribe: 订阅失败，状态码 %d", res.StatusCode)
	}

	s.setState(StateActive)
	s.startRefresh()
	return nil
}

// Unsubscribe 取消订阅（Expires: 0）。
func (s *Subscriber) Unsubscribe(ctx context.Context) error {
	s.stopRefresh()
	req := s.buildSubscribe(0)
	_, err := transport.DoRequest(ctx, s.cfg.Client, req)
	s.setState(StateIdle)
	return err
}

// Resubscribe 重新订阅（注册丢失后调用，WS-5 联动）。
func (s *Subscriber) Resubscribe(ctx context.Context) error {
	s.stopRefresh()
	return s.Subscribe(ctx)
}

// buildSubscribe 构造 SUBSCRIBE 请求。
func (s *Subscriber) buildSubscribe(expires int) *sip.Request {
	s.mu.RLock()
	callID := s.callID
	localTag := s.localTag
	s.mu.RUnlock()

	recipient := sip.Uri{Host: s.cfg.IMPU}
	recipient.UriParams = sip.HeaderParams{{K: "transport", V: "tcp"}}
	req := sip.NewRequest(sip.SUBSCRIBE, recipient)
	req.SetDestination(s.cfg.PCSCFAddr)

	// Call-ID（独立）
	req.AppendHeader(sip.NewHeader("Call-ID", callID))
	// From（带本地 tag）
	from := &sip.FromHeader{
		Address: sip.Uri{User: s.cfg.IMPU},
		Params:  sip.HeaderParams{{K: "tag", V: localTag}},
	}
	req.AppendHeader(from)
	// To
	to := &sip.ToHeader{Address: sip.Uri{User: s.cfg.IMPU}}
	req.AppendHeader(to)
	// Event
	req.AppendHeader(sip.NewHeader("Event", s.cfg.Event))
	// Expires
	req.AppendHeader(sip.NewHeader("Expires", fmt.Sprintf("%d", expires)))
	// Contact
	contact := &sip.ContactHeader{Address: sip.Uri{Host: s.cfg.Contact}}
	req.AppendHeader(contact)
	// Accept
	req.AppendHeader(sip.NewHeader("Accept", "application/reginfo+xml"))
	// Security-Verify（从 REGISTER 继承）
	if s.cfg.SecurityVerify != "" {
		req.AppendHeader(sip.NewHeader("Security-Verify", s.cfg.SecurityVerify))
	}
	req.AppendHeader(sip.NewHeader("Content-Length", "0"))

	s.mu.Lock()
	s.cseq++
	s.mu.Unlock()
	return req
}

// startRefresh 启动重订阅定时器。
func (s *Subscriber) startRefresh() {
	s.stopRefresh()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()

	interval := time.Duration(s.cfg.Expires/2) * time.Second
	if interval < 60*time.Second {
		interval = 60 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.Resubscribe(ctx); err != nil {
					s.log.Error("重订阅失败", "error", err)
					s.setState(StateFailed)
					return
				}
			}
		}
	}()
}

func (s *Subscriber) stopRefresh() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.mu.Unlock()
}
