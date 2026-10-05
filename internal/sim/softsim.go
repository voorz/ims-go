package sim

import "errors"

// NewSoftSIM 创建软件 SIM。keys 必须由 TestKeys / CustomTestKeys 构造。
func NewSoftSIM(imsi string, keys MilenageKeys) (*SoftSIM, error) {
	if !keys.Valid() {
		return nil, errors.New("sim: 软 SIM 密钥必须由 sim.TestKeys / sim.CustomTestKeys 构造")
	}
	m, err := NewMilenage(keys.k[:], keys.op[:], keys.useOPc)
	if err != nil {
		return nil, err
	}
	return &SoftSIM{imsi: imsi, milenage: m}, nil
}

// IMSI 返回配置的 IMSI。
func (s *SoftSIM) IMSI() string { return s.imsi }

// CalculateAKA 实现 AKAProvider：验证 AUTN，成功返回 RES/CK/IK 并前移 SQN；
// 失步时返回 ErrSyncFailure（附 AUTS）；MAC 失败返回 ErrAuthFailure。
func (s *SoftSIM) CalculateAKA(rand16, autn16 []byte) (AKAResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, ck, ik, auts, err := s.milenage.VerifyAUTN(rand16, autn16, s.sqn)
	if err != nil {
		return AKAResult{AUTS: auts}, err
	}
	// 从 AUTN 恢复 SQN 并前进一步，防重放。
	_, ak, err := s.milenage.F2F5(rand16)
	if err != nil {
		return AKAResult{}, err
	}
	var sqnBytes [6]byte
	for i := 0; i < 6; i++ {
		sqnBytes[i] = autn16[i] ^ ak[i]
	}
	s.sqn = decodeSQN(sqnBytes[:]) + 1
	return AKAResult{RES: res, CK: ck, IK: ik}, nil
}

// SQN 返回当前序列号。
func (s *SoftSIM) SQN() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sqn
}

// SetSQN 设置初始序列号（测试/实验室用）。
func (s *SoftSIM) SetSQN(sqn uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sqn = sqn
}
