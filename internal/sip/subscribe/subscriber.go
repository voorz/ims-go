package subscribe

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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

// SetSecurityVerify 更新 Security-Verify（REGISTER 成功后继承）。
func (s *Subscriber) SetSecurityVerify(v string) {
	s.mu.Lock()
	s.cfg.SecurityVerify = v
	s.mu.Unlock()
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
	return s.subscribeWithRetry(ctx, 0)
}

// subscribeWithRetry 带重建计数，防止 481 无限递归。
func (s *Subscriber) subscribeWithRetry(ctx context.Context, rebuildCount int) error {
	if rebuildCount > 2 {
		s.setState(StateFailed)
		return fmt.Errorf("subscribe: 481 重建超过上限")
	}
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
	// P0 修复：接受 200 和 202（RFC 3265 明确允许 202 Accepted）
	if res.StatusCode != 200 && res.StatusCode != 202 {
		// 481：订阅不存在，重建初始订阅（vowifi-go 生产做法）
		if res.StatusCode == 481 {
			s.log.Info("收到 481，重建初始订阅")
			s.setState(StateIdle)
			// 重置 dialog 状态，重建
			s.mu.Lock()
			s.callID = ""
			s.mu.Unlock()
			return s.subscribeWithRetry(ctx, rebuildCount+1)
		}
		// 403/405/489：永久拒绝，不重试（vowifi-go）
		if res.StatusCode == 403 || res.StatusCode == 405 || res.StatusCode == 489 {
			s.setState(StateFailed)
			return fmt.Errorf("subscribe: 永久拒绝，状态码 %d，不重试", res.StatusCode)
		}
		s.setState(StateFailed)
		return fmt.Errorf("subscribe: 订阅失败，状态码 %d", res.StatusCode)
	}
	// P0 修复：解析 Subscription-State，感知 terminated/pending
	if state := parseSubscriptionState(res); state == "terminated" {
		s.setState(StateFailed)
		return fmt.Errorf("subscribe: 订阅被终止 (Subscription-State: terminated)")
	}

	s.setState(StateActive)
	s.startRefresh()
	return nil
}

// parseSubscriptionStateFromRequest 从请求解析 Subscription-State。
// 格式：active;expires=3600 或 terminated;reason=timeout。
func parseSubscriptionStateFromRequest(req *sip.Request) string {
	return parseSubscriptionStateHeader(req.GetHeader("Subscription-State"))
}

// parseSubscriptionState 从响应解析 Subscription-State。
func parseSubscriptionState(res *sip.Response) string {
	return parseSubscriptionStateHeader(res.GetHeader("Subscription-State"))
}

// parseSubscriptionStateHeader 解析 Subscription-State 头值，返回 active/pending/terminated/""。
func parseSubscriptionStateHeader(h sip.Header) string {
	if h == nil {
		return ""
	}
	val := h.Value()
	if idx := strings.Index(val, ";"); idx > 0 {
		val = val[:idx]
	}
	return strings.TrimSpace(strings.ToLower(val))
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
	s.mu.Lock()
	callID := s.callID
	localTag := s.localTag
	cseq := s.cseq
	s.cseq++
	s.mu.Unlock()

	recipient := sip.Uri{Host: s.cfg.IMPU}
	recipient.UriParams = sip.HeaderParams{{K: "transport", V: "tcp"}}
	req := sip.NewRequest(sip.SUBSCRIBE, recipient)
	req.SetDestination(s.cfg.PCSCFAddr)

	// Call-ID（独立）
	req.AppendHeader(sip.NewHeader("Call-ID", callID))
	// CSeq（P0 修复：之前递增但从未写入，sipgo 填随机值导致 dialog 内不单调）
	req.AppendHeader(sip.NewHeader("CSeq", fmt.Sprintf("%d SUBSCRIBE", cseq)))
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
	s.mu.RLock()
	securityVerify := s.cfg.SecurityVerify
	s.mu.RUnlock()
	if securityVerify != "" {
		req.AppendHeader(sip.NewHeader("Security-Verify", securityVerify))
	}
	req.AppendHeader(sip.NewHeader("Content-Length", "0"))

	return req
}

// startRefresh 启动重订阅定时器。
func (s *Subscriber) startRefresh() {
	s.stopRefresh()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()

	// vowifi-go 做法：expires - 30s 缓冲（而不是 expires/2）。
	// expires/2 对 3600 是 1800s，太早重订浪费资源；-30s 精确。
	advance := 30 * time.Second
	interval := time.Duration(s.cfg.Expires)*time.Second - advance
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
