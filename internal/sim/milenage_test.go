package sim

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex 解码失败: %v", err)
	}
	return b
}

func testMilenage(t *testing.T) *Milenage {
	t.Helper()
	keys := TestKeys()
	m, err := NewMilenage(keys.k[:], keys.op[:], keys.useOPc)
	if err != nil {
		t.Fatalf("NewMilenage 失败: %v", err)
	}
	return m
}

func TestMilenageRoundTrip(t *testing.T) {
	m := testMilenage(t)
	rand := mustHex(t, "23553cbe9637a89d218ae64dae47bf35")
	sqn := mustHex(t, "ff9bb4d0b607")
	amf := mustHex(t, "b9b9")

	autn, err := m.GenerateAUTN(rand, sqn, amf)
	if err != nil {
		t.Fatalf("GenerateAUTN 失败: %v", err)
	}
	if len(autn) != 16 {
		t.Fatalf("AUTN 应为 16 字节，实际 %d", len(autn))
	}

	res, ck, ik, auts, err := m.VerifyAUTN(rand, autn, decodeSQN(sqn))
	if err != nil {
		t.Fatalf("VerifyAUTN 失败: %v", err)
	}
	if len(res) != 8 || len(ck) != 16 || len(ik) != 16 {
		t.Errorf("输出长度错误: RES=%d CK=%d IK=%d", len(res), len(ck), len(ik))
	}
	if auts != nil {
		t.Error("成功时 AUTS 应为 nil")
	}
}

func TestMilenageMACFailure(t *testing.T) {
	m := testMilenage(t)
	rand := mustHex(t, "23553cbe9637a89d218ae64dae47bf35")
	sqn := mustHex(t, "ff9bb4d0b607")
	amf := mustHex(t, "b9b9")

	autn, err := m.GenerateAUTN(rand, sqn, amf)
	if err != nil {
		t.Fatal(err)
	}
	autn[8] ^= 0xFF // 篡改 MAC-A

	_, _, _, _, err = m.VerifyAUTN(rand, autn, 0)
	if !errors.Is(err, ErrAuthFailure) {
		t.Errorf("MAC 篡改应返回 ErrAuthFailure，实际 %v", err)
	}
}

func TestMilenageSQNSyncFailure(t *testing.T) {
	m := testMilenage(t)
	rand := mustHex(t, "23553cbe9637a89d218ae64dae47bf35")
	sqn := mustHex(t, "ff9bb4d0b607")
	amf := mustHex(t, "b9b9")

	autn, err := m.GenerateAUTN(rand, sqn, amf)
	if err != nil {
		t.Fatal(err)
	}

	// 期望更大的 SQN → 失步
	_, _, _, auts, err := m.VerifyAUTN(rand, autn, decodeSQN(sqn)+100)
	if !errors.Is(err, ErrSyncFailure) {
		t.Fatalf("SQN 失步应返回 ErrSyncFailure，实际 %v", err)
	}
	if len(auts) != 14 {
		t.Errorf("AUTS 应为 14 字节，实际 %d", len(auts))
	}
}

func TestMilenageDeterministic(t *testing.T) {
	m := testMilenage(t)
	rand := mustHex(t, "23553cbe9637a89d218ae64dae47bf35")
	sqn := mustHex(t, "ff9bb4d0b607")
	amf := mustHex(t, "b9b9")

	macA1, macS1, err := m.F1(rand, sqn, amf)
	if err != nil {
		t.Fatal(err)
	}
	macA2, macS2, err := m.F1(rand, sqn, amf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(macA1, macA2) || !bytes.Equal(macS1, macS2) {
		t.Error("相同输入应产生相同输出")
	}
}

func TestMilenageBadLengths(t *testing.T) {
	m := testMilenage(t)
	if _, _, err := m.F1([]byte{1}, []byte{1}, []byte{1}); err == nil {
		t.Error("F1 非法长度应返回错误")
	}
	if _, _, err := m.F2F5([]byte{1}); err == nil {
		t.Error("F2F5 非法长度应返回错误")
	}
	if _, err := NewMilenage([]byte{1}, []byte{1}, false); err == nil {
		t.Error("非法密钥长度应返回错误")
	}
}

func TestMilenageKeysEnforcement(t *testing.T) {
	// 零值 MilenageKeys 无效：生产钥在结构上无法传入
	var zero MilenageKeys
	if zero.Valid() {
		t.Error("零值 MilenageKeys 应无效")
	}
	if _, err := NewSoftSIM("001010000000001", zero); err == nil {
		t.Error("无效密钥构造 SoftSIM 应失败")
	}
	if _, err := CustomTestKeys([]byte{1}, []byte{1}, false); err == nil {
		t.Error("非法长度的自定义测试密钥应失败")
	}
	keys, err := CustomTestKeys(bytes.Repeat([]byte{0xAB}, 16), bytes.Repeat([]byte{0xCD}, 16), false)
	if err != nil {
		t.Fatalf("合法自定义测试密钥不应失败: %v", err)
	}
	if !keys.Valid() {
		t.Error("合法构造的密钥应有效")
	}
}
