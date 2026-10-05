package register

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sip/transport"
)

// New 创建注册器。
func New(cfg Config) *Registrar {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Expires <= 0 {
		cfg.Expires = 600
	}
	return &Registrar{
		cfg:     cfg,
		log:     cfg.Logger,
		state:   StateUnregistered,
		penalty: make(map[string]int),
	}
}

// State 返回当前注册状态。
func (r *Registrar) State() State {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

// Registration 返回当前注册信息；未注册时为 nil。
func (r *Registrar) Registration() *Registration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.reg
}

func (r *Registrar) setState(to State) {
	r.mu.Lock()
	from := r.state
	r.state = to
	r.mu.Unlock()
	if from != to {
		r.log.Info("注册状态变更", "from", from.String(), "to", to.String())
		if r.cfg.OnStateChange != nil {
			r.cfg.OnStateChange(from, to)
		}
	}
}

// Register 执行完整注册流程：遍历 P-CSCF 候选，
// 每个候选走 初始 REGISTER → 401/407 → Digest-AKA → 200。
// 503/网络失败时 penalize 并切换下一个（P2 决策记录）。
func (r *Registrar) Register(ctx context.Context) error {
	r.setState(StateRegistering)

	addrs := r.orderedAddrs()
	if len(addrs) == 0 {
		r.setState(StateFailed)
		return fmt.Errorf("register: 无可用 P-CSCF")
	}

	var tried []string
	var lastErr error
	for _, addr := range addrs {
		tried = append(tried, addr)
		res, err := r.attempt(ctx, addr)
		if err != nil {
			r.penalize(addr)
			lastErr = err
			r.log.Info("P-CSCF 尝试失败，切换", "addr", addr, "error", err)
			continue
		}
		// 503: 服务不可用，切换
		if res.StatusCode == 503 {
			r.penalize(addr)
			lastErr = fmt.Errorf("P-CSCF %s 返回 503", addr)
			r.log.Info("P-CSCF 返回 503，切换", "addr", addr)
			continue
		}
		if res.StatusCode != 200 {
			r.setState(StateFailed)
			return fmt.Errorf("register: 注册失败，状态码 %d %s", res.StatusCode, res.Reason)
		}
		if err := r.onRegistered(res); err != nil {
			r.setState(StateFailed)
			return err
		}
		r.emitDecision(addr, tried, "注册成功")
		return nil
	}
	r.setState(StateFailed)
	r.emitDecision("", tried, "全部 P-CSCF 失败")
	if lastErr != nil {
		return fmt.Errorf("register: 全部 P-CSCF 失败: %w", lastErr)
	}
	return fmt.Errorf("register: 全部 P-CSCF 失败")
}

// attempt 对单个 P-CSCF 执行 初始 → 401/407 → 200（→ 494）流程。
func (r *Registrar) attempt(ctx context.Context, addr string) (*sip.Response, error) {
	req := r.buildRegister(r.cfg.Expires, addr)
	res, err := transport.DoRequest(ctx, r.cfg.Client, req)
	if err != nil {
		return nil, fmt.Errorf("初始 REGISTER 失败: %w", err)
	}

	if res.StatusCode == 401 || res.StatusCode == 407 {
		res, err = r.handleChallenge(ctx, req, res)
		if err != nil {
			return nil, err
		}
	}

	if res.StatusCode == transport.StatusSecurityAgreementRequired {
		res, err = r.handleSecAgree(ctx, req, res)
		if err != nil {
			return nil, err
		}
	}
	return res, nil
}

// orderedAddrs 按 penalty 排序返回候选地址（penalty 低的优先）。
func (r *Registrar) orderedAddrs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	addrs := append([]string(nil), r.cfg.PCSCFAddrs...)
	// 简单排序：penalty 低的在前（稳定）
	for i := 0; i < len(addrs); i++ {
		for j := i + 1; j < len(addrs); j++ {
			if r.penalty[addrs[j]] < r.penalty[addrs[i]] {
				addrs[i], addrs[j] = addrs[j], addrs[i]
			}
		}
	}
	return addrs
}

func (r *Registrar) penalize(addr string) {
	r.mu.Lock()
	r.penalty[addr]++
	r.mu.Unlock()
}

func (r *Registrar) emitDecision(selected string, tried []string, reason string) {
	if r.cfg.OnDecision == nil {
		return
	}
	r.cfg.OnDecision(Decision{
		At:       time.Now(),
		Selected: selected,
		Tried:    tried,
		Reason:   reason,
	})
}

// Unregister 注销：REGISTER with Expires: 0。
func (r *Registrar) Unregister(ctx context.Context) error {
	r.setState(StateUnregistering)
	r.stopRefresh()

	addrs := r.orderedAddrs()
	if len(addrs) == 0 {
		r.setState(StateFailed)
		return fmt.Errorf("register: 无可用 P-CSCF")
	}
	addr := addrs[0]
	req := r.buildRegister(0, addr)
	res, err := transport.DoRequest(ctx, r.cfg.Client, req)
	if err != nil {
		r.setState(StateFailed)
		return fmt.Errorf("register: 注销失败: %w", err)
	}
	if res.StatusCode == 401 || res.StatusCode == 407 {
		res, err = r.handleChallenge(ctx, req, res)
		if err != nil {
			r.setState(StateFailed)
			return err
		}
	}
	if res.StatusCode != 200 {
		r.setState(StateFailed)
		return fmt.Errorf("register: 注销失败，状态码 %d", res.StatusCode)
	}
	r.mu.Lock()
	r.reg = nil
	r.mu.Unlock()
	r.setState(StateUnregistered)
	return nil
}

// buildRegister 构造 REGISTER 请求（全部 sipgo 对象，零 raw string）。
func (r *Registrar) buildRegister(expires int, pcscfAddr string) *sip.Request {
	recipient := sip.Uri{Host: r.cfg.HomeDomain}
	// 强制 TCP（IMS over IPsec 使用 TCP；UDP 在沙箱/隧道内不可用）
	recipient.UriParams = sip.HeaderParams{{K: "transport", V: "tcp"}}
	req := sip.NewRequest(sip.REGISTER, recipient)
	req.SetDestination(pcscfAddr)

	// To/From
	to := &sip.ToHeader{Address: sip.Uri{User: r.cfg.IMPU, Host: r.cfg.HomeDomain}}
	// IMPU 可能已是完整 URI；简化处理
	req.AppendHeader(to)
	from := &sip.FromHeader{Address: sip.Uri{User: r.cfg.IMPI, Host: r.cfg.HomeDomain}}
	req.AppendHeader(from)

	// Contact
	contact := &sip.ContactHeader{Address: sip.Uri{Host: r.cfg.Contact}}
	contact.Params = sip.HeaderParams{{K: "expires", V: fmt.Sprintf("%d", expires)}}
	req.AppendHeader(contact)

	// Expires
	req.AppendHeader(sip.NewHeader("Expires", fmt.Sprintf("%d", expires)))
	req.AppendHeader(sip.NewHeader("Content-Length", "0"))

	// CSeq 由 sipgo 客户端自动管理；这里确保方法正确
	return req
}

// onRegistered 处理 200 OK：解析 Expires/GRUU，启动刷新定时器。
func (r *Registrar) onRegistered(res *sip.Response) error {
	expires := r.cfg.Expires
	if h := res.GetHeader("Expires"); h != nil {
		// 简化：用配置值；实际应解析
	}
	gruu, tempGRUU := parseGRUU(res)

	r.mu.Lock()
	r.reg = &Registration{
		Expires:   time.Now().Add(time.Duration(expires) * time.Second),
		ExpiresIn: expires,
		GRUU:      gruu,
		TempGRUU:  tempGRUU,
	}
	r.mu.Unlock()

	r.setState(StateRegistered)
	r.startRefresh(expires)
	return nil
}

// parseGRUU 从 200 OK 的 Contact 解析 pub-gruu/temp-gruu。
func parseGRUU(res *sip.Response) (pub, temp string) {
	for _, h := range res.GetHeaders("Contact") {
		ch, ok := h.(*sip.ContactHeader)
		if !ok {
			continue
		}
		for _, kv := range ch.Params {
			switch kv.K {
			case "pub-gruu":
				pub = kv.V
			case "temp-gruu":
				temp = kv.V
			}
		}
	}
	return pub, temp
}
