// Package stack 装配完整的 SIP 协议栈（WS-6/10 集成）。
//
// 之前 dialog.Registry、keepalive.Keeper、inbound.Dispatcher、transport.Pipeline
// 都是零集成的死代码。本包将它们与 register.Registrar、subscribe.Subscriber
// 装配为一个可 Start/Stop 的整体，实现 ims.Module 接口。
// 类型定义见 types.go（门禁③）。
package stack

import (
	"context"
	"log/slog"
	"sync"

	"github.com/emiago/sipgo"

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
	// VoiceHandler：入站 INVITE 的语音处理器（可为 nil）。
	VoiceHandler inbound.VoiceRequestHandler
	// OnRegisterState：注册状态变更回调。
	OnRegisterState func(from, to register.State)
	// OnSubscribeState：订阅状态变更回调。
	OnSubscribeState func(from, to subscribe.State)
	// OnKeepaliveFailed：保活失败回调（触发重注册）。
	OnKeepaliveFailed func()
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
