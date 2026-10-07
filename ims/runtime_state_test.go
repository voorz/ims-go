// Phase 4 验证：RuntimeState 6 灯流转 + Generation 防过期。
package ims

import (
	"testing"

	"github.com/voorz/ims-go/internal/sip/register"
)

func TestRuntimeState_Initial(t *testing.T) {
	c := &Client{}
	c.state.Store(int32(lcStopped))
	// 未启动时 State() 返回零值
	st := c.State()
	if st.SIMReady || st.TunnelReady || st.IMSReady {
		t.Fatalf("初始状态应全 false, got %+v", st)
	}
}

func TestRuntimeState_SetStage(t *testing.T) {
	c := &Client{}
	c.generation = 1

	// 正常更新
	ok := c.setRuntimeStage(1, StageTunnelReady, "隧道已建立", func(rs *RuntimeState) {
		rs.TunnelReady = true
	})
	if !ok {
		t.Fatal("setRuntimeStage 应成功")
	}
	st := c.State()
	if !st.TunnelReady {
		t.Fatal("TunnelReady 应为 true")
	}
	if st.Stage != StageTunnelReady {
		t.Fatalf("Stage 应为 %s, got %s", StageTunnelReady, st.Stage)
	}
	if st.Generation != 1 {
		t.Fatalf("Generation 应为 1, got %d", st.Generation)
	}

	// 过期代数更新应被拒绝
	ok = c.setRuntimeStage(0, StageIMSReady, "IMS", func(rs *RuntimeState) {
		rs.IMSReady = true
	})
	if ok {
		t.Fatal("过期代数的更新应被拒绝")
	}
	st = c.State()
	if st.IMSReady {
		t.Fatal("过期更新不应生效，IMSReady 应仍为 false")
	}
	// Stage 不应被过期更新污染
	if st.Stage != StageTunnelReady {
		t.Fatalf("Stage 不应被污染, got %s", st.Stage)
	}
}

func TestRuntimeState_Bidirectional(t *testing.T) {
	c := &Client{}
	c.generation = 1

	// 先置 true
	c.setRuntimeStage(1, StageTunnelReady, "隧道已建立", func(rs *RuntimeState) {
		rs.TunnelReady = true
	})
	// 再置 false（双向回落）
	c.setRuntimeStage(1, StageFailed, "隧道断开", func(rs *RuntimeState) {
		rs.TunnelReady = false
	})
	st := c.State()
	if st.TunnelReady {
		t.Fatal("双向语义：TunnelReady 应能从 true 回落到 false")
	}
}

func TestOnRegisterStateChange_Registered(t *testing.T) {
	c := &Client{
		disp:     newDispatcher(),
		modState: make(map[string]bool),
		cfg:      Config{},
	}
	c.generation = 1

	// 模拟注册成功
	c.onRegisterStateChange(register.StateRegistering, register.StateRegistered)

	st := c.State()
	if !st.IMSReady {
		t.Fatal("注册成功后 IMSReady 应为 true")
	}
	if st.Stage != StageIMSReady {
		t.Fatalf("Stage 应为 %s, got %s", StageIMSReady, st.Stage)
	}
}

func TestOnRegisterStateChange_Failed(t *testing.T) {
	c := &Client{
		disp:     newDispatcher(),
		modState: make(map[string]bool),
		cfg:      Config{},
	}
	c.generation = 1

	// 先成功，再失败
	c.onRegisterStateChange(register.StateRegistering, register.StateRegistered)
	c.onRegisterStateChange(register.StateRegistered, register.StateFailed)

	st := c.State()
	if st.IMSReady {
		t.Fatal("注册失败后 IMSReady 应为 false")
	}
	if st.LastErrorClass != "register" {
		t.Fatalf("LastErrorClass 应为 register, got %s", st.LastErrorClass)
	}
}
