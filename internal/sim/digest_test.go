package sim

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/icholy/digest"
)

var (
	testRAND     = bytesOf(0xAA, randAUTNLen)
	testAUTN     = bytesOf(0xBB, randAUTNLen)
	testRES      = []byte{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18}
	unsyncedRAND = bytesOf(0xCC, randAUTNLen)
	unsyncedAUTN = bytesOf(0xDD, randAUTNLen)
	testAUTS     = bytesOf(0xEE, 14)
)

func bytesOf(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// fakeProvider 模拟 SIM：只认识两个挑战，一个成功、一个失步。
type fakeProvider struct{}

func (fakeProvider) CalculateAKA(rand16, autn16 []byte) (AKAResult, error) {
	switch {
	case string(rand16) == string(testRAND) && string(autn16) == string(testAUTN):
		return AKAResult{RES: testRES, CK: bytesOf(0x21, 16), IK: bytesOf(0x22, 16)}, nil
	case string(rand16) == string(unsyncedRAND) && string(autn16) == string(unsyncedAUTN):
		return AKAResult{AUTS: testAUTS}, ErrSyncFailure
	default:
		return AKAResult{}, errors.New("fakeProvider: 无法识别的 RAND/AUTN")
	}
}

func nonceFor(randBytes, autnBytes []byte) string {
	return base64.StdEncoding.EncodeToString(append(append([]byte{}, randBytes...), autnBytes...))
}

func akaChallenge(nonce string) *digest.Challenge {
	return &digest.Challenge{
		Realm:     "ims.example.com",
		Nonce:     nonce,
		Algorithm: "AKAv1-MD5",
		QOP:       []string{"auth"},
		Opaque:    "opaque123",
	}
}

func TestComputeDigestSuccess(t *testing.T) {
	chal := akaChallenge(nonceFor(testRAND, testAUTN))
	params := DigestParams{
		Method:   "REGISTER",
		URI:      "sip:ims.example.com",
		Username: "user@ims.example.com",
		CNonce:   "cnonce123",
		Count:    1,
	}

	result, err := ComputeDigest(fakeProvider{}, chal, params)
	if err != nil {
		t.Fatalf("ComputeDigest 失败: %v", err)
	}
	if result.SyncFailure {
		t.Fatal("成功场景下 SyncFailure 应为 false")
	}
	if result.Algorithm != "AKAv1-MD5" {
		t.Errorf("wire 算法标签应恢复为 AKAv1-MD5，实际 %q", result.Algorithm)
	}
	if result.Nonce != chal.Nonce {
		t.Error("应原样返回 wire nonce")
	}

	// 用 RES 做口令独立重算期望响应
	mathChal := *chal
	mathChal.Algorithm = "MD5"
	want, err := digest.Digest(&mathChal, digest.Options{
		Method:   params.Method,
		URI:      params.URI,
		Username: params.Username,
		Password: string(testRES),
		Count:    params.Count,
		Cnonce:   params.CNonce,
	})
	if err != nil {
		t.Fatalf("独立重算失败: %v", err)
	}
	if result.Response != want.Response {
		t.Errorf("响应值错误：实际 %q，期望 %q", result.Response, want.Response)
	}
	if result.Username != params.Username || result.URI != params.URI {
		t.Errorf("用户名字段回填错误: %+v", result)
	}
}

func TestComputeDigestSyncFailure(t *testing.T) {
	chal := akaChallenge(nonceFor(unsyncedRAND, unsyncedAUTN))
	params := DigestParams{
		Method:   "REGISTER",
		URI:      "sip:ims.example.com",
		Username: "user@ims.example.com",
		CNonce:   "cnonce123",
		Count:    1,
	}

	result, err := ComputeDigest(fakeProvider{}, chal, params)
	if err != nil {
		t.Fatalf("ComputeDigest 失败: %v", err)
	}
	if !result.SyncFailure {
		t.Fatal("失步场景下 SyncFailure 应为 true")
	}
	if string(result.AUTS) != string(testAUTS) {
		t.Error("应原样返回 AUTS 字节")
	}

	// 空口令独立重算
	mathChal := *chal
	mathChal.Algorithm = "MD5"
	want, err := digest.Digest(&mathChal, digest.Options{
		Method:   params.Method,
		URI:      params.URI,
		Username: params.Username,
		Password: "",
		Count:    params.Count,
		Cnonce:   params.CNonce,
	})
	if err != nil {
		t.Fatalf("独立重算失败: %v", err)
	}
	if result.Response != want.Response {
		t.Errorf("空口令响应值错误：实际 %q，期望 %q", result.Response, want.Response)
	}
}

func TestComputeDigestProviderError(t *testing.T) {
	chal := akaChallenge(nonceFor(bytesOf(0x99, randAUTNLen), bytesOf(0x98, randAUTNLen)))
	_, err := ComputeDigest(fakeProvider{}, chal, DigestParams{Method: "REGISTER"})
	if err == nil {
		t.Fatal("provider 拒绝挑战时应返回错误")
	}
}

func TestComputeDigestMalformedNonce(t *testing.T) {
	for _, nonce := range []string{"", "!!!not-valid!!!", "aGVsbG8"} { // 最后一个太短
		chal := akaChallenge(nonce)
		_, err := ComputeDigest(fakeProvider{}, chal, DigestParams{Method: "REGISTER"})
		if err == nil {
			t.Errorf("非法 nonce %q 应返回错误", nonce)
		}
	}
}

func TestComputeDigestNilProvider(t *testing.T) {
	chal := akaChallenge(nonceFor(testRAND, testAUTN))
	_, err := ComputeDigest(nil, chal, DigestParams{Method: "REGISTER"})
	if err == nil {
		t.Fatal("AKA 挑战下 provider 为 nil 应返回错误")
	}
}

func TestComputeDigestPlainMD5(t *testing.T) {
	chal := &digest.Challenge{
		Realm:     "ims.example.com",
		Nonce:     "plain-nonce",
		Algorithm: "MD5",
	}
	result, err := ComputeDigest(nil, chal, DigestParams{
		Method:   "REGISTER",
		URI:      "sip:ims.example.com",
		Username: "user",
	})
	if err != nil {
		t.Fatalf("纯 MD5 不应需要 provider: %v", err)
	}
	if result.Algorithm != "MD5" {
		t.Errorf("算法标签应为 MD5，实际 %q", result.Algorithm)
	}
	if result.Response == "" {
		t.Error("应算出响应值")
	}
	if !strings.Contains(result.Nonce, "plain-nonce") {
		t.Error("应原样返回 nonce")
	}
}
