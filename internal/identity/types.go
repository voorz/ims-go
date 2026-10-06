package identity

import (
	"github.com/voorz/ims-go/internal/carrier"
)

// Identity 是解析后的身份。
type Identity struct {
	IMPI string // 私有标识
	IMPU string // 公开标识
	Mode IdentityMode
	MCC  string
	MNC  string
}

// PrepareInput 是 PrepareStart 输入。
type PrepareInput struct {
	// MCC/MNC 是 PLMN。
	MCC string
	MNC string
	// IMPI/IMPU 为空时按 Mode 推导。
	IMPI string
	IMPU string
	Mode IdentityMode
	// Resolver 是运营商解析器。
	Resolver *carrier.Resolver
}

// IdentityMode 是身份三态。
type IdentityMode int

const (
	// ModeISIM 使用 ISIM 卡身份。
	ModeISIM IdentityMode = iota
	// ModeAuto 自动（优先 ISIM，无则推导）。
	ModeAuto
	// ModeDerived 推导身份。
	ModeDerived
)
