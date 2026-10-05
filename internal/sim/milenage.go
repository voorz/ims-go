package sim

import (
	"crypto/aes"
	"crypto/subtle"
	"encoding/binary"
	"errors"
)

// NewMilenage 创建 Milenage 实例。
// k: 128 位用户密钥；op: 128 位运营商密钥（OP 或 OPc）；
// useOPc 为 true 时 op 参数直接作为 OPc，否则按 OP 计算 OPc。
func NewMilenage(k, op []byte, useOPc bool) (*Milenage, error) {
	if len(k) != 16 || len(op) != 16 {
		return nil, errors.New("milenage: K 与 OP/OPc 必须为 16 字节")
	}
	m := &Milenage{}
	copy(m.k[:], k)
	copy(m.op[:], op)
	if useOPc {
		copy(m.opc[:], op)
	} else if err := m.computeOPc(); err != nil {
		return nil, err
	}
	return m, nil
}

// computeOPc 计算 OPc = AES_K(OP) ⊕ OP。
func (m *Milenage) computeOPc() error {
	cipher, err := aes.NewCipher(m.k[:])
	if err != nil {
		return err
	}
	var encrypted [16]byte
	cipher.Encrypt(encrypted[:], m.op[:])
	for i := 0; i < 16; i++ {
		m.opc[i] = encrypted[i] ^ m.op[i]
	}
	return nil
}

// tempBlock 计算 TEMP = AES_K(RAND ⊕ OPc)，各 f 函数的公共前导。
func (m *Milenage) tempBlock(rand []byte) ([16]byte, error) {
	var temp [16]byte
	cipher, err := aes.NewCipher(m.k[:])
	if err != nil {
		return temp, err
	}
	for i := 0; i < 16; i++ {
		temp[i] = rand[i] ^ m.opc[i]
	}
	cipher.Encrypt(temp[:], temp[:])
	return temp, nil
}

// outBlock 计算 OUT = AES_K(rot(TEMP ⊕ OPc, r) ⊕ c) ⊕ OPc。
func (m *Milenage) outBlock(temp [16]byte, rotBits int, c uint8) ([16]byte, error) {
	var out [16]byte
	cipher, err := aes.NewCipher(m.k[:])
	if err != nil {
		return out, err
	}
	var tmp [16]byte
	for i := 0; i < 16; i++ {
		tmp[i] = temp[i] ^ m.opc[i]
	}
	tmp = rotate(tmp, rotBits)
	tmp[15] ^= c
	cipher.Encrypt(tmp[:], tmp[:])
	for i := 0; i < 16; i++ {
		tmp[i] ^= m.opc[i]
	}
	return tmp, nil
}

// F1 计算网络认证码 MAC-A 与重同步认证码 MAC-S。
// 输入：RAND(16)、SQN(6)、AMF(2)；输出：MAC-A(8)、MAC-S(8)。
func (m *Milenage) F1(rand, sqn, amf []byte) (macA, macS []byte, err error) {
	if len(rand) != 16 || len(sqn) != 6 || len(amf) != 2 {
		return nil, nil, errors.New("milenage: F1 参数长度错误")
	}
	temp, err := m.tempBlock(rand)
	if err != nil {
		return nil, nil, err
	}
	// IN1 = SQN || AMF || SQN || AMF；r1=64，c1=0
	var in1 [16]byte
	copy(in1[0:6], sqn)
	copy(in1[6:8], amf)
	copy(in1[8:14], sqn)
	copy(in1[14:16], amf)
	out, err := m.outBlockXor(temp, 64, 0, in1)
	if err != nil {
		return nil, nil, err
	}
	return append([]byte(nil), out[0:8]...), append([]byte(nil), out[8:16]...), nil
}

// outBlockXor 是带 IN 异或的 outBlock 变体（仅 f1 需要）。
func (m *Milenage) outBlockXor(temp [16]byte, rotBits int, c uint8, in [16]byte) ([16]byte, error) {
	var out [16]byte
	cipher, err := aes.NewCipher(m.k[:])
	if err != nil {
		return out, err
	}
	var tmp [16]byte
	for i := 0; i < 16; i++ {
		tmp[i] = temp[i] ^ m.opc[i]
	}
	tmp = rotate(tmp, rotBits)
	for i := 0; i < 16; i++ {
		tmp[i] ^= in[i]
	}
	tmp[15] ^= c
	cipher.Encrypt(tmp[:], tmp[:])
	for i := 0; i < 16; i++ {
		tmp[i] ^= m.opc[i]
	}
	return tmp, nil
}

// F2F5 计算响应 RES(8) 与匿名密钥 AK(6)。r2=0，c2=1。
func (m *Milenage) F2F5(rand []byte) (res, ak []byte, err error) {
	if len(rand) != 16 {
		return nil, nil, errors.New("milenage: F2F5 的 RAND 必须为 16 字节")
	}
	temp, err := m.tempBlock(rand)
	if err != nil {
		return nil, nil, err
	}
	out, err := m.outBlock(temp, 0, 1)
	if err != nil {
		return nil, nil, err
	}
	return append([]byte(nil), out[8:16]...), append([]byte(nil), out[0:6]...), nil
}

// F3 计算加密密钥 CK(16)。r3=32，c3=2。
func (m *Milenage) F3(rand []byte) ([]byte, error) {
	if len(rand) != 16 {
		return nil, errors.New("milenage: F3 的 RAND 必须为 16 字节")
	}
	temp, err := m.tempBlock(rand)
	if err != nil {
		return nil, err
	}
	out, err := m.outBlock(temp, 32, 2)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), out[:]...), nil
}

// F4 计算完整性密钥 IK(16)。r4=64，c4=4。
func (m *Milenage) F4(rand []byte) ([]byte, error) {
	if len(rand) != 16 {
		return nil, errors.New("milenage: F4 的 RAND 必须为 16 字节")
	}
	temp, err := m.tempBlock(rand)
	if err != nil {
		return nil, err
	}
	out, err := m.outBlock(temp, 64, 4)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), out[:]...), nil
}

// F5Star 计算重同步用匿名密钥 AK*(6)。r5=96，c5=8。
func (m *Milenage) F5Star(rand []byte) ([]byte, error) {
	if len(rand) != 16 {
		return nil, errors.New("milenage: F5* 的 RAND 必须为 16 字节")
	}
	temp, err := m.tempBlock(rand)
	if err != nil {
		return nil, err
	}
	out, err := m.outBlock(temp, 96, 8)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), out[0:6]...), nil
}

// GenerateAUTN 生成认证令牌 AUTN = (SQN ⊕ AK) || AMF || MAC-A。
func (m *Milenage) GenerateAUTN(rand, sqn, amf []byte) ([]byte, error) {
	_, ak, err := m.F2F5(rand)
	if err != nil {
		return nil, err
	}
	macA, _, err := m.F1(rand, sqn, amf)
	if err != nil {
		return nil, err
	}
	autn := make([]byte, 16)
	for i := 0; i < 6; i++ {
		autn[i] = sqn[i] ^ ak[i]
	}
	copy(autn[6:8], amf)
	copy(autn[8:16], macA)
	return autn, nil
}

// VerifyAUTN 验证 AUTN，成功返回 RES/CK/IK；
// MAC 校验失败返回 ErrAuthFailure；SQN 失步返回 ErrSyncFailure（附 AUTS）。
func (m *Milenage) VerifyAUTN(rand, autn []byte, expectedSQN uint64) (res, ck, ik, auts []byte, err error) {
	if len(rand) != 16 || len(autn) != 16 {
		return nil, nil, nil, nil, errors.New("milenage: RAND/AUTN 必须为 16 字节")
	}
	res, ak, err := m.F2F5(rand)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	sqn := make([]byte, 6)
	for i := 0; i < 6; i++ {
		sqn[i] = autn[i] ^ ak[i]
	}
	amf := autn[6:8]
	macA, _, err := m.F1(rand, sqn, amf)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if subtle.ConstantTimeCompare(macA, autn[8:16]) != 1 {
		return nil, nil, nil, nil, ErrAuthFailure
	}
	if decodeSQN(sqn) < expectedSQN {
		auts, err := m.GenerateAUTS(rand, sqn)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		return nil, nil, nil, auts, ErrSyncFailure
	}
	if ck, err = m.F3(rand); err != nil {
		return nil, nil, nil, nil, err
	}
	if ik, err = m.F4(rand); err != nil {
		return nil, nil, nil, nil, err
	}
	return res, ck, ik, nil, nil
}

// GenerateAUTS 生成重同步参数 AUTS = (SQN ⊕ AK*) || MAC-S。
func (m *Milenage) GenerateAUTS(rand, sqn []byte) ([]byte, error) {
	akStar, err := m.F5Star(rand)
	if err != nil {
		return nil, err
	}
	_, macS, err := m.F1(rand, sqn, []byte{0x00, 0x00})
	if err != nil {
		return nil, err
	}
	auts := make([]byte, 14)
	for i := 0; i < 6; i++ {
		auts[i] = sqn[i] ^ akStar[i]
	}
	copy(auts[6:14], macS)
	return auts, nil
}

// rotate 循环左移 bits 位（3GPP 的 r 均为 8 的倍数，按字节轮转即精确）。
func rotate(data [16]byte, bits int) [16]byte {
	var result [16]byte
	byteShift := (bits / 8) % 16
	for i := 0; i < 16; i++ {
		result[i] = data[(i+byteShift)%16]
	}
	return result
}

func decodeSQN(data []byte) uint64 {
	if len(data) < 6 {
		return 0
	}
	return uint64(data[0])<<40 | uint64(data[1])<<32 |
		uint64(data[2])<<24 | uint64(data[3])<<16 |
		uint64(data[4])<<8 | uint64(data[5])
}

// EncodeSQN 把 SQN 编码为 6 字节。
func EncodeSQN(sqn uint64) []byte {
	buf := make([]byte, 6)
	binary.BigEndian.PutUint16(buf[0:2], uint16(sqn>>32))
	binary.BigEndian.PutUint32(buf[2:6], uint32(sqn))
	return buf
}
