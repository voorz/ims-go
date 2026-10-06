// Package stack 装配完整的 SIP 协议栈（WS-6/10 集成）。
//
// 之前 dialog.Registry、keepalive.Keeper、inbound.Dispatcher、transport.Pipeline
// 都是零集成的死代码。本包将它们与 register.Registrar、subscribe.Subscriber
// 装配为一个可 Start/Stop 的整体，实现 ims.Module 接口。
// 类型定义见 types.go（门禁③）。
package stack

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sip/dialog"
	"github.com/voorz/ims-go/internal/sip/inbound"
	"github.com/voorz/ims-go/internal/sip/keepalive"
	"github.com/voorz/ims-go/internal/sip/register"
	"github.com/voorz/ims-go/internal/sip/subscribe"
	"github.com/voorz/ims-go/internal/sip/transport"
)

// New 创建 SIP 协议栈（未启动）。
func New(cfg Config) (*Stack, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if len(cfg.PCSCFAddrs) == 0 {
		return nil, fmt.Errorf("stack: P-CSCF 地址为空")
	}
	if cfg.IMPU == "" {
		return nil, fmt.Errorf("stack: IMPU 为空")
	}

	s := &Stack{cfg: cfg, log: cfg.Logger}

	// 1. sipgo UserAgent（Client + Server）
	ua, err := sipgo.NewUA()
	if err != nil {
		return nil, fmt.Errorf("stack: 创建 UA: %w", err)
	}
	s.ua = ua
	s.client, err = sipgo.NewClient(ua)
	if err != nil {
		return nil, fmt.Errorf("stack: 创建 Client: %w", err)
	}
	s.server, err = sipgo.NewServer(ua)
	if err != nil {
		return nil, fmt.Errorf("stack: 创建 Server: %w", err)
	}

	// 2. dialog 注册表
	s.dialogs = dialog.NewRegistry(cfg.Logger)

	// 3. 入站分发器（注册到 Server）
	s.dispatcher = inbound.New(cfg.Logger, cfg.VoiceHandler)
	s.dispatcher.Register(s.server)

	// 4. 传输 Pipeline
	s.pipeline = transport.New(transport.Config{
		Dialer:           cfg.Dialer,
		OnConnectionLost: cfg.OnConnectionLost,
		Logger:           cfg.Logger,
	})

	// 5. Registrar（REGISTER）
	s.registrar = register.New(register.Config{
		IMPU:                  cfg.IMPU,
		IMPI:                  cfg.IMPI,
		HomeDomain:            cfg.HomeDomain,
		PCSCFAddrs:            cfg.PCSCFAddrs,
		Contact:               cfg.Contact,
		Expires:               cfg.RegisterExpires,
		AKAProvider:           cfg.AKAProvider,
		EAPRES:                cfg.EAPRES,
		EnableVariantFallback: true,
		Client:                s.client,
		VariantStore:          cfg.VariantStore,
		OnStateChange:         cfg.OnRegisterState,
		Logger:                cfg.Logger,
	})

	// 6. Subscriber（SUBSCRIBE reg）
	// SecurityVerify 不在此时设置：REGISTER 成功后经 SetSecurityVerify 继承
	//（从 200 OK 的 Security-Server 提取）。
	s.subscriber = subscribe.New(subscribe.Config{
		IMPU:          cfg.IMPU,
		Event:         "reg",
		Expires:       cfg.SubscribeExpires,
		PCSCFAddr:     cfg.PCSCFAddrs[0],
		Contact:       cfg.Contact,
		Client:        s.client,
		Server:        s.server,
		OnStateChange: cfg.OnSubscribeState,
		Logger:        cfg.Logger,
	})

	// 7. Keepalive（OPTIONS）
	s.keeper = keepalive.New(keepalive.Config{
		Target: cfg.PCSCFAddrs[0],
		Sender: func(ctx context.Context, req *sip.Request) (*sip.Response, error) {
			return transport.DoRequest(ctx, s.client, req)
		},
		OnFailed: func() {
			s.log.Warn("keepalive 连续失败，触发重注册")
			// 联动 WS-5：保活失败说明传输可能已断，触发完整重注册。
			// 异步执行，避免阻塞 keepalive 循环。
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				if err := s.registrar.Register(ctx); err != nil {
					s.log.Error("保活触发的重注册失败", "error", err)
					if s.cfg.OnKeepaliveFailed != nil {
						s.cfg.OnKeepaliveFailed()
					}
				} else {
					s.log.Info("保活触发的重注册成功")
				}
			}()
		},
		Logger: cfg.Logger,
	})

	return s, nil
}

// Start 启动协议栈：连接 → REGISTER → SUBSCRIBE → keepalive。
func (s *Stack) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("stack: 已在运行")
	}
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()

	// 1. 传输连接（Pipeline）：预拨号到 P-CSCF，单连接复用（R1），
	// 连接丢失触发 OnConnectionLost（R3）。
	// 注意：Dialer 为 nil 时用 net.Dialer 直连（仅测试）；
	// 生产需注入经 IPsec 隧道的 Dialer（P2）。
	pcscfAddr := s.cfg.PCSCFAddrs[0]
	if err := s.pipeline.Connect(ctx, s.ua.TransportLayer(), pcscfAddr); err != nil {
		cancel()
		return fmt.Errorf("stack: 传输连接 %s 失败: %w", pcscfAddr, err)
	}
	s.log.Info("SIP 传输已连接", "pcscf", pcscfAddr)

	// 2. IMS REGISTER
	if err := s.registrar.Register(ctx); err != nil {
		cancel()
		return fmt.Errorf("stack: REGISTER 失败: %w", err)
	}
	s.log.Info("IMS 注册成功")

	// 2.5 Security-Verify 继承：从 REGISTER 200 OK 提取 Security-Server，
	// 传给 SUBSCRIBE（之前是"简化处理"未实现）。
	if reg := s.registrar.Registration(); reg != nil && reg.SecurityServer != "" {
		s.subscriber.SetSecurityVerify(reg.SecurityServer)
		s.log.Info("Security-Verify 已继承", "server", reg.SecurityServer)
	}

	// 3. SUBSCRIBE(reg)
	if err := s.subscriber.Subscribe(ctx); err != nil {
		s.log.Warn("SUBSCRIBE 失败（继续运行）", "error", err)
		// 订阅失败不阻塞主流程（P-CSCF 保活靠 keepalive）。
	}

	// 4. Keepalive 启动
	s.keeper.Start()

	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	return nil
}

// Stop 停止协议栈。
func (s *Stack) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.mu.Unlock()

	// 逆序停止
	if s.keeper != nil {
		s.keeper.Stop()
	}
	if s.subscriber != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.subscriber.Unsubscribe(ctx)
		cancel()
	}
	if s.pipeline != nil {
		_ = s.pipeline.Close()
	}

	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
	s.log.Info("SIP 协议栈已停止")
	return nil
}

// Registrar 返回注册器（供外部查询状态）。
func (s *Stack) Registrar() *register.Registrar { return s.registrar }

// Subscriber 返回订阅器。
func (s *Stack) Subscriber() *subscribe.Subscriber { return s.subscriber }

// Dialogs 返回 dialog 注册表。
func (s *Stack) Dialogs() *dialog.Registry { return s.dialogs }

// SIPClient 返回内部 sipgo 客户端（供 SMS/USSD 等模块复用传输）。
func (s *Stack) SIPClient() *sipgo.Client { return s.client }

// SIPServer 返回内部 sipgo 服务端（供 SMS/USSD 等模块注册入站处理）。
func (s *Stack) SIPServer() *sipgo.Server { return s.server }
