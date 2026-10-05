package sim

import (
	"errors"
	"sync"
)

// 本文件集中存放 sim 包的导出类型（红线#9）。

// AKAResult 是一次 AKA 计算的结果（3GPP TS 33.102 / RFC 4187）。
type AKAResult struct {
	RES  []byte // 认证响应
	CK   []byte // 加密密钥
	IK   []byte // 完整性密钥
	AUTS []byte // 重同步令牌（同步失败时）
}

// AKAProvider 从网络挑战（RAND、AUTN）计算 AKA。
// 硬件 SIM 由消费方实现并注入（D-010）；软件 SIM 见 SoftSIM（D-015）。
type AKAProvider interface {
	CalculateAKA(rand16, autn16 []byte) (AKAResult, error)
}

// ISIMAKAProvider 从 ISIM 应用计算 AKA（3GPP TS 31.103）。
type ISIMAKAProvider interface {
	CalculateISIMAKA(rand16, autn16 []byte) (AKAResult, error)
}

var (
	// ErrSyncFailure 表示 AUTN 序列号失步；AKAResult.AUTS 携带重同步令牌。
	ErrSyncFailure = errors.New("sim: AKA synchronisation failure")
	// ErrAuthFailure 表示 SIM 拒绝了网络认证（MAC 校验失败）。
	ErrAuthFailure = errors.New("sim: AKA authentication failure")
	// ErrAPDUBusy 表示 SIM 的 APDU 通道暂时无法服务 AKA。
	ErrAPDUBusy = errors.New("sim: APDU channel busy")
)

// DigestParams 是 ComputeDigest 的输入参数。
type DigestParams struct {
	Method   string // SIP 方法，如 "REGISTER"
	URI      string // digest-uri，如 "sip:ims.example.com"
	Username string // 如 IMPI
	CNonce   string // 客户端 nonce，可空（为空时自动生成）
	Count    int    // nonce-count
}

// DigestResult 是一轮 RFC 3310 Digest AKAv1-MD5 计算的结果。
// 注意：返回的是各字段的**值**，而非拼好的 Authorization 头字符串；
// 由 sip 侧用 sipgo 对象装配成头（红线#1）。
type DigestResult struct {
	Username    string
	Realm       string
	Nonce       string // 收到的原始 wire nonce
	URI         string
	Response    string // 十六进制 digest 响应值
	Algorithm   string // wire 标签，如 "AKAv1-MD5"
	CNonce      string
	Opaque      string
	Qop         string
	NonceCount  int
	SyncFailure bool   // 为 true 时 Response 为空口令响应，AUTS 携带重同步令牌
	AUTS        []byte // 原始 AUTS 字节（仅 SyncFailure 时）
}

// MilenageKeys 是软 SIM 的密钥材料（D-015）。
// 字段未导出：只能通过 TestKeys / CustomTestKeys 构造，
// 生产钥在结构上无法传入软件侧。
type MilenageKeys struct {
	k      [16]byte
	op     [16]byte
	useOPc bool
	ok     bool
}

// TestKeys 返回 3GPP TS 35.208 测试集 1 的测试密钥，仅用于测试/实验室。
func TestKeys() MilenageKeys {
	var k, op [16]byte
	copy(k[:], []byte{
		0x46, 0x5b, 0x5c, 0xe8, 0xb1, 0x99, 0xb4, 0x9f,
		0xaa, 0x5f, 0x0a, 0x2e, 0xe2, 0x38, 0xa6, 0xbc,
	})
	copy(op[:], []byte{
		0xcd, 0xc2, 0x02, 0xd5, 0x12, 0x3e, 0x20, 0xf6,
		0x2b, 0x6d, 0x67, 0x6a, 0xc7, 0x2c, 0xb3, 0x18,
	})
	return MilenageKeys{k: k, op: op, ok: true}
}

// CustomTestKeys 构造自定义测试密钥。
// 警告：仅用于测试/实验室环境；生产环境的密钥必须走硬件 SIM（AKAProvider 注入），
// 绝不能传入软件侧。
func CustomTestKeys(k, op []byte, useOPc bool) (MilenageKeys, error) {
	if len(k) != 16 || len(op) != 16 {
		return MilenageKeys{}, errors.New("sim: 测试密钥 K 与 OP/OPc 必须为 16 字节")
	}
	var kk, oo [16]byte
	copy(kk[:], k)
	copy(oo[:], op)
	return MilenageKeys{k: kk, op: oo, useOPc: useOPc, ok: true}, nil
}

// Valid 报告密钥是否由 TestKeys / CustomTestKeys 构造。
func (k MilenageKeys) Valid() bool { return k.ok }

// Milenage 实现 3GPP TS 35.206 的认证算法（f1/f2/f3/f4/f5*），
// 用于无物理 SIM 卡场景的 EAP-AKA/IMS-AKA（D-015）。
// 从 swu-go 移植，修正：密钥字段未导出、MAC 常量时间比较、
// 返回哨兵错误（ErrAuthFailure/ErrSyncFailure）。
type Milenage struct {
	k   [16]byte // 128 位用户密钥
	op  [16]byte // 128 位运营商变体字段
	opc [16]byte // 派生：OPc = AES_K(OP) ⊕ OP
}

// SoftSIM 是基于 milenage 的软件 SIM（D-015）。
//
// 默认关闭、显式 opt-in（Config.SIM.SoftSIM.Enable）；密钥只能来自
// TestKeys / CustomTestKeys（MilenageKeys 字段未导出），生产钥在结构上
// 无法传入软件侧。用途：CI 全链路 e2e（fake ePDG + 真 AKA）与实验室
// 参数调优；生产环境请使用硬件 SIM（AKAProvider 注入）。
type SoftSIM struct {
	imsi     string
	milenage *Milenage
	mu       sync.Mutex
	sqn      uint64
}
