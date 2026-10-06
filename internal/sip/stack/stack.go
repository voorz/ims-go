// Package stack 装配完整的 SIP 协议栈（WS-6/10 集成）。
//
// 之前 dialog.Registry、keepalive.Keeper、inbound.Dispatcher、transport.Pipeline
// 都是零集成的死代码。本包将它们与 register.Registrar、subscribe.Subscriber
// 装配为一个可 Start/Stop 的整体，实现 ims.Module 接口。
package stack

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"

	"github.com/voorz/ims-go/internal/sim"
	"github.com/voorz/ims-go/internal/sip/dialog"
	"github.com/voorz/ims-go/internal/sip/inbound"
	"github.com/voorz/ims-go/internal/sip/keepalive"
	"github.com/voorz/ims-go/internal/sip/register"
	"github.com/voorz/ims-go/internal/sip/subscribe"
	"github.com/voorz/ims-go/internal/sip/transport"
)

// Config 是 SIP 协议栈配置。
type Config struct {
	// IMPU/IMPI/HomeDomain：IMS 标识。
	IMPU       string
	IMPI       string
	HomeDomain string
	// PCSCFAddrs：P-CSCF 候选。
	PCSCFAddrs []string
	// Contact：本地 Contact。
	Contact string
	// RegisterExpires：注册有效期（秒）；0 用默认。
	RegisterExpires int
	// SubscribeExpires：订阅有效期（秒）；0 用默认 3600。
	SubscribeExpires int
	// AKAProvider：Digest-AKA。
	AKAProvider sim.AKAProvider
	// EAPRES：SWu 阶段 EAP-AKA RES（可选）。
	EAPRES string
	// SecurityVerify：从 REGISTER 继承的 Security-Server（订阅用）。
	SecurityVerify string
	// VoiceHandler：入站 INVITE 的语音处理器（可为 nil）。
	VoiceHandler inbound.VoiceRequestHandler
	// OnRegisterState：注册状态变更回调。
	OnRegisterState func(from, to register.State)
	// OnSubscribeState：订阅状态变更回调。
	OnSubscribeState func(from, to subscribe.State)
	// Logger：为空用 slog 默认。
	Logger *slog.Logger
}

// Stack 是装配后的 SIP 协议栈。
type Stack struct {
	cfg Config
	log *slog.Logger

	mu         sync.Mutex
	ua         *sipgo.UserAgent
	client     *sipgo.Client
	server     *sipgo.Server
	pipeline   *transport.Pipeline
	dialogs    *dialog.Registry
	registrar  *register.Registrar
	subscriber *subscribe.Subscriber
	keeper     *keepalive.Keeper
	dispatcher *inbound.Dispatcher

	running bool
	cancel  context.CancelFunc
}

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
		Logger: cfg.Logger,
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
		OnStateChange:         cfg.OnRegisterState,
		Logger:                cfg.Logger,
	})

	// 6. Subscriber（SUBSCRIBE reg）
	s.subscriber = subscribe.New(subscribe.Config{
		IMPU:           cfg.IMPU,
		Event:          "reg",
		Expires:        cfg.SubscribeExpires,
		SecurityVerify: cfg.SecurityVerify,
		PCSCFAddr:      cfg.PCSCFAddrs[0],
		Contact:        cfg.Contact,
		Client:         s.client,
		Server:         s.server,
		OnStateChange:  cfg.OnSubscribeState,
		Logger:         cfg.Logger,
	})

	// 7. Keepalive（OPTIONS）
	s.keeper = keepalive.New(keepalive.Config{
		Target: cfg.PCSCFAddrs[0],
		Sender: func(ctx context.Context, req *sip.Request) (*sip.Response, error) {
			return transport.DoRequest(ctx, s.client, req)
		},
		OnFailed: func() {
			s.log.Warn("keepalive 连续失败，触发恢复")
			// TODO: 联动 WS-5 重新注册（P2）。
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

	// 1. 传输连接（Pipeline）
	// 注意：Pipeline.Connect 需要 TransportLayer，实际由 sipgo 管理。
	// 此处简化：Client 直接可用（sipgo 延迟拨号）。
	s.log.Info("SIP 协议栈启动", "pcscf", s.cfg.PCSCFAddrs[0])

	// 2. IMS REGISTER
	if err := s.registrar.Register(ctx); err != nil {
		cancel()
		return fmt.Errorf("stack: REGISTER 失败: %w", err)
	}
	s.log.Info("IMS 注册成功")

	// 3. SUBSCRIBE(reg)
	// Security-Verify 从注册结果继承（此处简化，实际应从 200 OK 提取）。
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*1000000000)
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
