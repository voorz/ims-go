package sim

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/icholy/digest"
)

// randAUTNLen 是 3GPP AKA 中 RAND 与 AUTN 各自的固定长度。
const randAUTNLen = 16

// ComputeDigest 执行一轮 RFC 3310 Digest AKAv1-MD5：
// 从挑战的 nonce 中解出 RAND/AUTN，请 provider 计算 AKA，
// 算出 digest 响应**各字段的值**（由调用方用 sipgo 对象装配成 Authorization 头）。
//
//   - 成功：HA1 = MD5(username:realm:RES)，用原始 RES 字节做口令；
//     其余与 RFC 2617 完全相同。
//   - 同步失败：用空口令计算响应，并附带 AUTS（RFC 3310 §3.4）；
//     调用方应期待服务端重同步后下发新的挑战，重试循环由调用方驱动。
//
// 注意：icholy/digest 只认固定算法白名单（"", "MD5", "SHA-256", ...），
// 会直接拒绝 "AKAv1-MD5"，因此数学计算时强制 Algorithm="MD5"
// （RFC 3310 明确指出二者等价），wire 标签在返回结果中恢复。
func ComputeDigest(provider AKAProvider, chal *digest.Challenge, params DigestParams) (DigestResult, error) {
	wireAlgorithm := chal.Algorithm
	if wireAlgorithm == "" {
		wireAlgorithm = "AKAv1-MD5"
	}

	if strings.EqualFold(wireAlgorithm, "MD5") {
		// 纯 MD5 Digest：nonce 是不透明字符串，不解码、不需要 AKA。
		mathChal := *chal
		mathChal.Algorithm = "MD5"
		cred, err := digest.Digest(&mathChal, toOptions(params, ""))
		if err != nil {
			return DigestResult{}, fmt.Errorf("sim: 计算纯 digest 失败: %w", err)
		}
		return fromCredentials(cred, wireAlgorithm, false, nil), nil
	}

	// 以下为 AKA 路径：nonce = base64(RAND || AUTN [|| server data])
	mathChal := *chal
	mathChal.Algorithm = "MD5"

	rawNonce, err := decodeNonceBytes(chal.Nonce)
	if err != nil {
		return DigestResult{}, err
	}

	if provider == nil {
		return DigestResult{}, errors.New("sim: AKA 挑战需要 AKAProvider")
	}

	rand16, autn16, err := splitNonce(rawNonce)
	if err != nil {
		return DigestResult{}, err
	}

	akaResult, akaErr := provider.CalculateAKA(rand16, autn16)

	switch {
	case akaErr == nil:
		cred, err := digest.Digest(&mathChal, toOptions(params, string(akaResult.RES)))
		if err != nil {
			return DigestResult{}, fmt.Errorf("sim: 计算 digest 失败: %w", err)
		}
		return fromCredentials(cred, wireAlgorithm, false, nil), nil

	case errors.Is(akaErr, ErrSyncFailure):
		cred, err := digest.Digest(&mathChal, toOptions(params, ""))
		if err != nil {
			return DigestResult{}, fmt.Errorf("sim: 计算重同步 digest 失败: %w", err)
		}
		return fromCredentials(cred, wireAlgorithm, true, akaResult.AUTS), nil

	default:
		return DigestResult{}, fmt.Errorf("sim: CalculateAKA 失败: %w", akaErr)
	}
}

func toOptions(params DigestParams, password string) digest.Options {
	cnonce := params.CNonce
	if cnonce == "" {
		var b [8]byte
		if _, err := rand.Read(b[:]); err == nil {
			cnonce = hex.EncodeToString(b[:])
		}
	}
	return digest.Options{
		Method:   params.Method,
		URI:      params.URI,
		Username: params.Username,
		Password: password,
		Count:    params.Count,
		Cnonce:   cnonce,
	}
}

func fromCredentials(cred *digest.Credentials, wireAlgorithm string, syncFailure bool, auts []byte) DigestResult {
	return DigestResult{
		Username:    cred.Username,
		Realm:       cred.Realm,
		Nonce:       cred.Nonce,
		URI:         cred.URI,
		Response:    cred.Response,
		Algorithm:   wireAlgorithm,
		CNonce:      cred.Cnonce,
		Opaque:      cred.Opaque,
		Qop:         cred.QOP,
		NonceCount:  cred.Nc,
		SyncFailure: syncFailure,
		AUTS:        auts,
	}
}

func decodeNonceBytes(nonce string) ([]byte, error) {
	trimmed := strings.TrimSpace(nonce)
	if trimmed == "" {
		return nil, errors.New("sim: 空 nonce")
	}
	if len(trimmed)%2 == 0 && isASCIIHex(trimmed) {
		if raw, err := hex.DecodeString(trimmed); err == nil {
			return raw, nil
		}
	}
	raw, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil {
		padded := trimmed
		for len(padded)%4 != 0 {
			padded += "="
		}
		raw, err = base64.StdEncoding.DecodeString(padded)
		if err != nil {
			return nil, fmt.Errorf("sim: 解码 nonce 失败: %w", err)
		}
	}
	return raw, nil
}

// splitNonce 把 RFC 3310 AKAv1-MD5 nonce（base64(RAND || AUTN [|| server data])）
// 拆成 RAND/AUTN 两部分。
func splitNonce(raw []byte) (rand16, autn16 []byte, err error) {
	if len(raw) < 2*randAUTNLen {
		return nil, nil, fmt.Errorf("sim: nonce 太短，容不下 RAND||AUTN（%d 字节）", len(raw))
	}
	return raw[:randAUTNLen], raw[randAUTNLen : 2*randAUTNLen], nil
}

func isASCIIHex(value string) bool {
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}
