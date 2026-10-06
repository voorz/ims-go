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
	r := &Registrar{
		cfg:     cfg,
		log:     cfg.Logger,
		state:   StateUnregistered,
		penalty: make(map[string]int),
	}
	// P1：从持久化存储加载已学习的变体
	if cfg.VariantStore != nil && cfg.IMPU != "" {
		if variant, err := cfg.VariantStore.LoadVariant(cfg.IMPU); err == nil && variant != "" {
			r.learnedVariant = variant
			r.log.Info("从存储加载已学习变体", "variant", variant)
		}
	}
	return r
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

// Register 执行完整注册流程：变体矩阵 × P-CSCF 候选的二维试错。
//
// 决策流水线（超越想法）：
//  1. 协议级重试（Retry-After/423）→ 等待后重试
//  2. 换变体（403/配置码）→ 下一个 REGISTER 花样
//  3. 换 P-CSCF（503/网络失败）→ 下一个候选（带 penalty）
func (r *Registrar) Register(ctx context.Context) error {
	r.setState(StateRegistering)

	addrs := r.orderedAddrs()
	if len(addrs) == 0 {
		r.setState(StateFailed)
		return fmt.Errorf("register: 无可用 P-CSCF")
	}

	var tried []string
	var lastErr error
	const maxSameAddrRetries = 3
	for _, addr := range addrs {
		tried = append(tried, addr)
		// 同一 P-CSCF 可重试（Retry-After/423），上限 3 次
	retryLoop:
		for retry := 0; retry < maxSameAddrRetries; retry++ {
			res, err := r.attempt(ctx, addr)
			if err != nil {
				// 网络错误：penalize 并换下一个
				r.penalize(addr)
				lastErr = err
				r.log.Info("P-CSCF 尝试失败，切换", "addr", addr, "error", err)
				break // 跳出重试循环，换下一个 addr
			}
			// 200：成功（先判断，不走失败决策）
			if res.StatusCode == 200 {
				if err := r.onRegistered(res); err != nil {
					r.setState(StateFailed)
					return err
				}
				r.emitDecision(addr, tried, "注册成功")
				return nil
			}
			// 305 Use-Proxy：用 Contact 指定的代理重试
			if res.StatusCode == 305 {
				if proxy := parseUseProxy(res); proxy != "" {
					r.log.Info("305 Use-Proxy，切换代理", "proxy", proxy)
					addr = proxy // 本轮继续用新代理
					continue
				}
			}
			// 423 Min-Expires：调整 expires 后重试当前 P-CSCF
			if res.StatusCode == 423 {
				if minExpires := parseMinExpires(headersToMap(res)); minExpires > 0 {
					r.mu.Lock()
					r.cfg.Expires = minExpires
					r.mu.Unlock()
					r.log.Info("423 Min-Expires，调整后重试", "expires", minExpires)
					continue // 重试当前 addr
				}
			}
			// 二维失败决策
			decision := DecideFailure(res.StatusCode, headersToMap(res), 0, 1, true)
			// reachedAuth 守卫：已到认证阶段，不再切换 P-CSCF
			//（认证失败是变体/凭证问题，换 P-CSCF 无用）。
			r.mu.RLock()
			reachedAuth := r.reachedAuth
			r.mu.RUnlock()
			if reachedAuth && decision.AdvanceRegistrar {
				r.log.Info("已到认证阶段，不切换 P-CSCF", "addr", addr, "reason", decision.Reason)
				r.setState(StateFailed)
				return fmt.Errorf("register: 认证失败 %d（%s），不切换 P-CSCF", res.StatusCode, decision.Reason)
			}
			switch {
			case decision.AdvanceRegistrar:
				r.penalize(addr)
				lastErr = fmt.Errorf("P-CSCF %s: %s", addr, decision.Reason)
				r.log.Info("切换 P-CSCF", "addr", addr, "reason", decision.Reason)
				break retryLoop // 跳出重试循环，换下一个 addr
			case decision.RetryAfter > 0:
				r.log.Info("等待后重试当前 P-CSCF", "addr", addr, "after", decision.RetryAfter, "reason", decision.Reason)
				select {
				case <-ctx.Done():
					r.setState(StateFailed)
					return ctx.Err()
				case <-time.After(decision.RetryAfter):
				}
				continue // 重试当前 addr
			case decision.GiveUp:
				r.setState(StateFailed)
				return fmt.Errorf("register: %s", decision.Reason)
			}
			// 未覆盖的状态码：失败
			r.setState(StateFailed)
			return fmt.Errorf("register: 注册失败，状态码 %d %s", res.StatusCode, res.Reason)
		}
	}
	r.setState(StateFailed)
	r.emitDecision("", tried, "全部 P-CSCF 失败")
	if lastErr != nil {
		return fmt.Errorf("register: 全部 P-CSCF 失败: %w", lastErr)
	}
	return fmt.Errorf("register: 全部 P-CSCF 失败")
}

// headersToMap 将 sip 响应头转为 map（用于失败决策）。
func headersToMap(res *sip.Response) map[string]string {
	m := make(map[string]string)
	// TODO: 遍历所有头；当前只取关键头
	if h := res.GetHeader("Retry-After"); h != nil {
		m["Retry-After"] = h.Value()
	}
	if h := res.GetHeader("Min-Expires"); h != nil {
		m["Min-Expires"] = h.Value()
	}
	return m
}

// attempt 对单个 P-CSCF 执行变体矩阵试错：
// 每个变体走 初始 REGISTER → 401/407 → 200（→ 494）流程。
// 403/配置的状态码触发下一个变体；503/网络失败触发换 P-CSCF。
func (r *Registrar) attempt(ctx context.Context, addr string) (*sip.Response, error) {
	variants := r.variantsForAttempt()
	var lastErr error
	for i, v := range variants {
		r.log.Info("尝试 REGISTER 变体", "variant", v.Name, "index", i, "total", len(variants))
		res, err := r.attemptVariant(ctx, addr, v)
		if err != nil {
			lastErr = err
			// 网络错误：换 P-CSCF，不换变体
			return nil, err
		}
		// 503：换 P-CSCF
		if res.StatusCode == 503 {
			return res, nil
		}
		// 403 或配置的状态码：换变体
		if r.shouldTryNextVariant(res.StatusCode) && i+1 < len(variants) {
			r.log.Info("变体被拒绝，尝试下一个", "variant", v.Name, "status", res.StatusCode)
			lastErr = fmt.Errorf("变体 %s 被拒绝: %d", v.Name, res.StatusCode)
			continue
		}
		return res, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("所有变体均失败")
}

// variantsForAttempt 返回本次尝试的变体矩阵。
func (r *Registrar) variantsForAttempt() []Variant {
	variants := Variants()
	// EAP RES 可用时，eap_direct 插到首位
	if r.cfg.EAPRES != "" {
		variants = append([]Variant{EAPDirectVariant()}, variants...)
	}
	// P1 变体学习：已学习的成功变体优先（O(1) 命中）
	r.mu.RLock()
	learned := r.learnedVariant
	r.mu.RUnlock()
	if learned != "" {
		for i, v := range variants {
			if v.Name == learned {
				// 移到首位
				variants = append([]Variant{v}, append(variants[:i], variants[i+1:]...)...)
				r.log.Info("变体学习命中", "variant", learned)
				break
			}
		}
	}
	// 未启用 fallback 时只用 base
	if !r.cfg.EnableVariantFallback && len(variants) > 0 {
		// 保留 eap_direct（如果有）+ base
		filtered := variants[:0]
		for _, v := range variants {
			if v.Name == "base" || v.IsEAPDirect() {
				filtered = append(filtered, v)
			}
		}
		variants = filtered
	}
	return variants
}

// learnVariant 记录成功的变体（P1 学习）：内存 + 持久化。
func (r *Registrar) learnVariant(name string) {
	r.mu.Lock()
	r.learnedVariant = name
	r.mu.Unlock()
	r.log.Info("学习成功变体", "variant", name)
	// 持久化（失败不致命）
	if r.cfg.VariantStore != nil && r.cfg.IMPU != "" {
		if err := r.cfg.VariantStore.SaveVariant(r.cfg.IMPU, name); err != nil {
			r.log.Warn("变体持久化失败", "error", err)
		}
	}
}

// shouldTryNextVariant 判断状态码是否触发换变体。
func (r *Registrar) shouldTryNextVariant(statusCode int) bool {
	// 403 总是触发换变体
	if statusCode == 403 {
		return true
	}
	// TODO: 可配置的状态码列表（vowifi-core 的 InitialRejectFallbackStatusCodes）
	return false
}

// attemptVariant 用指定变体执行单次 初始 → 401/407 → 200（→ 494）流程。
func (r *Registrar) attemptVariant(ctx context.Context, addr string, v Variant) (*sip.Response, error) {
	req := r.buildRegister(r.cfg.Expires, addr, v)
	res, err := transport.DoRequest(ctx, r.cfg.Client, req)
	if err != nil {
		return nil, fmt.Errorf("初始 REGISTER 失败: %w", err)
	}

	if res.StatusCode == 401 || res.StatusCode == 407 {
		// 到达认证阶段：标记 reachedAuth，后续不再切换 P-CSCF
		//（vowifi-core 生产经验：认证阶段的失败是变体/凭证问题，不是 P-CSCF 问题）。
		r.mu.Lock()
		r.reachedAuth = true
		r.mu.Unlock()
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

	// 成功：学习变体，保存 protected refresh 所需信息
	if res.StatusCode == 200 {
		r.learnVariant(v.Name)
		r.saveRefreshState(req, res)
	}
	return res, nil
}

// saveRefreshState 保存 protected refresh 所需的状态。
// 仅当请求带 Authorization 时保存（即已认证）。
func (r *Registrar) saveRefreshState(req *sip.Request, res *sip.Response) {
	authHeader := req.GetHeader("Authorization")
	if authHeader == nil {
		authHeader = req.GetHeader("Proxy-Authorization")
	}
	if authHeader == nil {
		return // 未认证，不保存
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if h := req.GetHeader("Call-ID"); h != nil {
		r.lastCallID = h.Value()
	}
	if h := req.GetHeader("CSeq"); h != nil {
		var seq int
		fmt.Sscanf(h.Value(), "%d", &seq)
		r.lastCSeq = seq
	}
	r.lastAuth = authHeader.Value()
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
	// 注销用 base 变体（不带认证花样）
	req := r.buildRegister(0, addr, Variant{Name: "base"})
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
// variant 控制初始 Authorization 模式、PANI 和蜂窝信息。
func (r *Registrar) buildRegister(expires int, pcscfAddr string, v Variant) *sip.Request {
	recipient := sip.Uri{Host: r.cfg.HomeDomain}
	// 强制 TCP（IMS over IPsec 使用 TCP；UDP 在沙箱/隧道内不可用）
	recipient.UriParams = sip.HeaderParams{{K: "transport", V: "tcp"}}
	req := sip.NewRequest(sip.REGISTER, recipient)
	req.SetDestination(pcscfAddr)

	// To/From
	to := &sip.ToHeader{Address: sip.Uri{User: r.cfg.IMPU, Host: r.cfg.HomeDomain}}
	req.AppendHeader(to)
	from := &sip.FromHeader{Address: sip.Uri{User: r.cfg.IMPI, Host: r.cfg.HomeDomain}}
	req.AppendHeader(from)

	// Contact
	contact := &sip.ContactHeader{Address: sip.Uri{Host: r.cfg.Contact}}
	contact.Params = sip.HeaderParams{{K: "expires", V: fmt.Sprintf("%d", expires)}}
	req.AppendHeader(contact)

	// Expires
	req.AppendHeader(sip.NewHeader("Expires", fmt.Sprintf("%d", expires)))

	// 初始 Authorization（按变体模式）
	if auth := r.buildInitialAuth(v); auth != nil {
		req.AppendHeader(auth)
	}

	// P-Access-Network-Info（按变体）
	if v.IncludePANI {
		// TODO: 从网络状态获取实际 PANI；当前用占位
		req.AppendHeader(sip.NewHeader("P-Access-Network-Info", "IEEE-802.11"))
	}

	req.AppendHeader(sip.NewHeader("Content-Length", "0"))

	// 通用头重排（如配置了 HeaderOrder）
	applyHeaderOrder(req, r.cfg.HeaderOrder)
	return req
}

// applyHeaderOrder 按 order 重排请求头顺序。
// order 中的头按指定顺序排在前面；未在 order 中的头保持原相对顺序追加。
// 这是通用机制，不针对特定运营商硬编码。
func applyHeaderOrder(req *sip.Request, order []string) {
	if len(order) == 0 {
		return
	}
	// 收集所有头（克隆，避免修改原切片）
	type hdr struct {
		name   string
		header sip.Header
	}
	var all []hdr
	for _, h := range req.Headers() {
		all = append(all, hdr{name: h.Name(), header: h})
	}
	// 按 order 排序：在 order 中的按 order 索引，不在的放后面保持原序
	orderIdx := make(map[string]int, len(order))
	for i, name := range order {
		orderIdx[name] = i
	}
	// 稳定排序（冒泡，保持简单可读）
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			oi, iIn := orderIdx[all[i].name]
			oj, jIn := orderIdx[all[j].name]
			if jIn && (!iIn || oj < oi) {
				all[i], all[j] = all[j], all[i]
			}
		}
	}
	// 清除原有头（逐个删除）
	seen := make(map[string]bool)
	for _, h := range all {
		if !seen[h.name] {
			req.RemoveHeader(h.name)
			seen[h.name] = true
		}
	}
	// 按新顺序追加
	for _, h := range all {
		req.AppendHeader(h.header)
	}
}

// buildInitialAuth 按变体模式构造初始 Authorization 头。
// 返回 nil 表示不带认证头。
func (r *Registrar) buildInitialAuth(v Variant) sip.Header {
	switch v.InitialAuth {
	case "eap_direct":
		// EAP 直接认证：复用 SWu RES
		// 格式：Digest username=IMPI, realm=homeDomain, nonce="", uri=..., response=EAPRES
		// 这是 vowifi-core 的生产做法，避免 USIM SQN 双消耗
		if r.cfg.EAPRES == "" {
			return nil
		}
		return sip.NewHeader("Authorization",
			fmt.Sprintf(`Digest username="%s", realm="%s", nonce="", uri="sip:%s", response="%s", algorithm=AKAv1-MD5`,
				r.cfg.IMPI, r.cfg.HomeDomain, r.cfg.HomeDomain, r.cfg.EAPRES))
	case "aka_empty":
		// 空 Digest-AKA 占位
		return sip.NewHeader("Authorization",
			fmt.Sprintf(`Digest username="%s", realm="%s", nonce="", uri="sip:%s", response="", algorithm=AKAv1-MD5`,
				r.cfg.IMPI, r.cfg.HomeDomain, r.cfg.HomeDomain))
	case "aka_empty_uri_first":
		// 空 Digest-AKA，URI 优先（某些运营商要求）
		return sip.NewHeader("Authorization",
			fmt.Sprintf(`Digest uri="sip:%s", username="%s", realm="%s", nonce="", response="", algorithm=AKAv1-MD5`,
				r.cfg.HomeDomain, r.cfg.IMPI, r.cfg.HomeDomain))
	case "aka_zero_response_uri_first":
		// response=0 的占位，URI 优先
		return sip.NewHeader("Authorization",
			fmt.Sprintf(`Digest uri="sip:%s", username="%s", realm="%s", nonce="", response="0", algorithm=AKAv1-MD5`,
				r.cfg.HomeDomain, r.cfg.IMPI, r.cfg.HomeDomain))
	case "none", "":
		// 不带认证头
		return nil
	default:
		return nil
	}
}

// onRegistered 处理 200 OK：解析 Expires/GRUU/Security-Server，启动刷新定时器。
func (r *Registrar) onRegistered(res *sip.Response) error {
	expires := r.cfg.Expires
	if h := res.GetHeader("Expires"); h != nil {
		// 简化：用配置值；实际应解析
	}
	gruu, tempGRUU := parseGRUU(res)
	// Security-Server：SUBSCRIBE 继承用
	securityServer := ""
	if h := res.GetHeader("Security-Server"); h != nil {
		securityServer = h.Value()
	}

	r.mu.Lock()
	r.reg = &Registration{
		Expires:        time.Now().Add(time.Duration(expires) * time.Second),
		ExpiresIn:      expires,
		GRUU:           gruu,
		TempGRUU:       tempGRUU,
		SecurityServer: securityServer,
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
