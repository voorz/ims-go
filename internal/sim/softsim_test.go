package sim

import (
	"errors"
	"sync"
	"testing"
)

// TestSoftSIMCalculateAKA 用真 milenage 做 AKA：
// SoftSIM 生成 AUTN（扮演网络侧），再用 CalculateAKA 验证（扮演 UE 侧）。
func TestSoftSIMCalculateAKA(t *testing.T) {
	keys := TestKeys()
	m, err := NewMilenage(keys.k[:], keys.op[:], keys.useOPc)
	if err != nil {
		t.Fatal(err)
	}
	ue, err := NewSoftSIM("001010000000001", keys)
	if err != nil {
		t.Fatal(err)
	}

	rand := bytesOf(0x11, 16)
	sqn := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01}
	amf := []byte{0x80, 0x00}
	autn, err := m.GenerateAUTN(rand, sqn, amf)
	if err != nil {
		t.Fatal(err)
	}

	result, err := ue.CalculateAKA(rand, autn)
	if err != nil {
		t.Fatalf("CalculateAKA 失败: %v", err)
	}
	if len(result.RES) != 8 || len(result.CK) != 16 || len(result.IK) != 16 {
		t.Errorf("AKA 结果长度错误: %+v", result)
	}
	if ue.SQN() != 2 {
		t.Errorf("SQN 应前移到 2，实际 %d", ue.SQN())
	}

	// 同一 AUTN 重放：SQN 已前移，应失步
	_, err = ue.CalculateAKA(rand, autn)
	if !errors.Is(err, ErrSyncFailure) {
		t.Errorf("重放旧 AUTN 应返回 ErrSyncFailure，实际 %v", err)
	}
}

func TestSoftSIMConcurrent(t *testing.T) {
	ue, err := NewSoftSIM("001010000000001", TestKeys())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ue.SQN()
			ue.SetSQN(42)
		}()
	}
	wg.Wait()
	if ue.SQN() != 42 {
		t.Errorf("并发读写后 SQN 应为 42，实际 %d", ue.SQN())
	}
}
