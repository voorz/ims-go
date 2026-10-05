package swu

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/voorz/ims-go/internal/swu/ikev2"
)

// 以下 helper 原定义于已按 D-006 排除的 restoration 测试文件中，
// 行为测试（fragment_flow_test.go）仍需要它们。

const fragmentTestMTU = 300

func newFragmentTestSession(aead bool) *Session {
	session := NewSession(&Config{})
	session.spiI = [8]byte{7}
	session.spiR = [8]byte{9}
	session.ikeKeys = testIKEKeys()
	session.mu.Lock()
	session.fragmentationSupported = true
	session.ikeFragmentMTU = fragmentTestMTU
	session.mu.Unlock()
	if aead {
		session.encrAlg = uint16(ikev2.ENCR_AES_GCM_16)
		session.integAlg = uint16(ikev2.AUTH_NONE)
		session.aead = true
		session.ikeKeys.SK_ei = bytes.Repeat([]byte{0x33}, 20)
		session.ikeKeys.SK_er = bytes.Repeat([]byte{0x44}, 20)
	}
	return session
}

func fragmentTestPayload(size int) *ikev2.RawPayload {
	return &ikev2.RawPayload{PType: ikev2.V, Data: bytes.Repeat([]byte{0xa5}, size)}
}

func decodeFragmentMetadata(
	t *testing.T,
	data []byte,
) (*ikev2.IKEHeader, *ikev2.PayloadHeader, uint16, uint16) {
	t.Helper()
	header, generic, err := decodeSKFHeaders(data)
	if err != nil {
		t.Fatalf("decodeSKFHeaders() error = %v", err)
	}
	return header, generic, binary.BigEndian.Uint16(data[32:34]), binary.BigEndian.Uint16(data[34:36])
}
