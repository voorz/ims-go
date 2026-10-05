package ims

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeModule 是测试替身：Start 阻塞在 block 上，关闭后返回 startErr。
type fakeModule struct {
	starts   atomic.Int32
	stops    atomic.Int32
	block    chan struct{}
	startErr error
}

func newFakeModule() *fakeModule { return &fakeModule{block: make(chan struct{})} }

func (f *fakeModule) Start(ctx context.Context) error {
	f.starts.Add(1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.block:
		return f.startErr
	}
}

func (f *fakeModule) Stop() error {
	f.stops.Add(1)
	return nil
}

type fakeSMSModule struct {
	fakeModule
	mu   sync.Mutex
	sent []SMSRequest
}

func (f *fakeSMSModule) Send(ctx context.Context, req SMSRequest) (*SMSResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, req)
	return &SMSResult{MessageID: "msg-1", Segments: 1}, nil
}

type fakeVoiceModule struct {
	fakeModule
	mu     sync.Mutex
	hungUp []string
}

func (f *fakeVoiceModule) Dial(ctx context.Context, req CallRequest) (*Call, error) {
	return &Call{ID: "call-1"}, nil
}

func (f *fakeVoiceModule) Hangup(ctx context.Context, callID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hungUp = append(f.hungUp, callID)
	return nil
}

// waitFor 轮询 cond 直到成立或超时。
func waitFor(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("超时未达成: %s", msg)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestClient_Lifecycle(t *testing.T) {
	c, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Status().State; got != StateStopped {
		t.Fatalf("初始状态应为 stopped，实际 %q", got)
	}

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if got := c.Status().State; got != StateRunning {
		t.Fatalf("Start 后状态应为 running，实际 %q", got)
	}

	if err := c.Start(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("重复 Start 应返回 ErrAlreadyRunning，实际 %v", err)
	}

	if err := c.Stop(); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	if got := c.Status().State; got != StateStopped {
		t.Fatalf("Stop 后状态应为 stopped，实际 %q", got)
	}
	if err := c.Stop(); err != nil {
		t.Fatalf("Stop 应幂等，实际 %v", err)
	}
}

func TestClient_Supervision_RestartOnFailure(t *testing.T) {
	fm := newFakeModule()
	fm.startErr = errors.New("boom")
	close(fm.block) // Start 立即返回错误

	cfg := Config{}
	cfg.Recovery.InitialBackoff = 10 * time.Millisecond
	cfg.Recovery.MaxBackoff = 50 * time.Millisecond
	cfg.Recovery.MaxRestarts = 3
	cfg.Modules.Tunnel = fm

	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	events := make(chan Event, 64)
	unsub := c.OnEvent(func(e Event) { events <- e })
	defer unsub()

	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	// 等待监督器放弃重启（第 4 次失败后 give-up）
	waitFor(t, 5*time.Second, "监督器放弃重启", func() bool {
		for _, d := range c.Decisions() {
			if d.Point == "module-recovery" && d.Chosen == "give-up" {
				return true
			}
		}
		return false
	})

	if got := fm.starts.Load(); got < 2 {
		t.Errorf("模块失败后应被重启，Start 调用次数=%d", got)
	}

	// 应收到过 module.failed 事件
	sawFailed := false
drain:
	for {
		select {
		case e := <-events:
			if e.Type == EventModuleFailed && e.Module == "tunnel" {
				sawFailed = true
			}
		default:
			break drain
		}
	}
	if !sawFailed {
		t.Error("应收到 module.failed 事件")
	}
}

func TestClient_Supervision_DisabledNoRestart(t *testing.T) {
	fm := newFakeModule()
	fm.startErr = errors.New("boom")
	close(fm.block)

	cfg := Config{}
	cfg.Recovery.Disabled = true
	cfg.Recovery.InitialBackoff = 10 * time.Millisecond
	cfg.Modules.Tunnel = fm

	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	time.Sleep(200 * time.Millisecond)
	if got := fm.starts.Load(); got != 1 {
		t.Errorf("禁用恢复时 Start 应只调用 1 次，实际 %d", got)
	}
}

func TestClient_SendSMS_Delegates(t *testing.T) {
	sms := &fakeSMSModule{fakeModule: *newFakeModule()}
	cfg := Config{}
	cfg.Modules.SMS = sms

	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	res, err := c.SendSMS(context.Background(), SMSRequest{To: "+8613800000000", Text: "hi"})
	if err != nil {
		t.Fatalf("SendSMS 失败: %v", err)
	}
	if res.MessageID != "msg-1" || res.Segments != 1 {
		t.Errorf("SendSMS 结果错误: %+v", res)
	}
	sms.mu.Lock()
	defer sms.mu.Unlock()
	if len(sms.sent) != 1 || sms.sent[0].To != "+8613800000000" {
		t.Errorf("SMS 模块未收到正确请求: %+v", sms.sent)
	}
}

func TestClient_SendSMS_NoModule(t *testing.T) {
	c, _ := New(Config{})
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	if _, err := c.SendSMS(context.Background(), SMSRequest{}); !errors.Is(err, ErrNoSMSModule) {
		t.Errorf("未装配 SMS 模块应返回 ErrNoSMSModule，实际 %v", err)
	}
	if _, err := c.SendUSSD(context.Background(), "*100#"); !errors.Is(err, ErrNoUSSDModule) {
		t.Errorf("未装配 USSD 模块应返回 ErrNoUSSDModule，实际 %v", err)
	}
	if _, err := c.Voice().Dial(context.Background(), CallRequest{}); !errors.Is(err, ErrNoVoiceModule) {
		t.Errorf("未装配语音模块应返回 ErrNoVoiceModule，实际 %v", err)
	}
}

func TestClient_Voice_DialHangup(t *testing.T) {
	vm := &fakeVoiceModule{fakeModule: *newFakeModule()}
	cfg := Config{}
	cfg.Modules.Voice = vm

	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	call, err := c.Voice().Dial(context.Background(), CallRequest{To: "sip:bob@example.com"})
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}
	if call.ID != "call-1" {
		t.Errorf("Call ID 错误: %q", call.ID)
	}
	if err := call.Hangup(context.Background()); err != nil {
		t.Fatalf("Hangup 失败: %v", err)
	}
	vm.mu.Lock()
	defer vm.mu.Unlock()
	if len(vm.hungUp) != 1 || vm.hungUp[0] != "call-1" {
		t.Errorf("语音模块未收到正确挂断: %v", vm.hungUp)
	}
}

func TestClient_Status_ListsModules(t *testing.T) {
	cfg := Config{}
	cfg.Modules.Tunnel = newFakeModule()
	cfg.Modules.SMS = &fakeSMSModule{fakeModule: *newFakeModule()}

	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	st := c.Status()
	if st.State != StateRunning {
		t.Errorf("状态应为 running，实际 %q", st.State)
	}
	// 模块状态由监督 goroutine 异步标记，轮询等待收敛
	waitFor(t, 2*time.Second, "模块状态收敛为运行中", func() bool {
		st := c.Status()
		if len(st.Modules) != 2 {
			return false
		}
		for _, m := range st.Modules {
			if !m.Running {
				return false
			}
		}
		return true
	})
}

func TestClient_Decisions_Capped(t *testing.T) {
	c, _ := New(Config{})
	for i := 0; i < maxDecisionRecords+100; i++ {
		c.RecordDecision(DecisionRecord{Point: "test", Chosen: "x"})
	}
	if got := len(c.Decisions()); got != maxDecisionRecords {
		t.Errorf("决策记录应截断为 %d，实际 %d", maxDecisionRecords, got)
	}
}
