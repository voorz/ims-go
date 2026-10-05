// Package identity 提供身份准备（WS-13，H9）。
//
// PrepareStart：Profile 校验 → 运营商解析 → 身份三态（isim/auto/derived）。
// 实现下沉到同一包，消除 toInternal/fromInternal（D-007）。
package identity

import (
	"fmt"
	"log/slog"

	"github.com/voorz/ims-go/internal/carrier"
)

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

func (m IdentityMode) String() string {
	switch m {
	case ModeISIM:
		return "isim"
	case ModeAuto:
		return "auto"
	case ModeDerived:
		return "derived"
	default:
		return "unknown"
	}
}

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

// PrepareStart 执行启动前准备（H9：准备+启动一体）。
func PrepareStart(log *slog.Logger, in PrepareInput) (*Identity, *carrier.CarrierConfig, error) {
	if log == nil {
		log = slog.Default()
	}
	// 1. Profile 校验
	if in.MCC == "" || in.MNC == "" {
		return nil, nil, fmt.Errorf("identity: MCC/MNC 不能为空")
	}
	if in.Resolver == nil {
		return nil, nil, fmt.Errorf("identity: Resolver 不能为空")
	}

	// 2. 运营商解析
	cfg, err := in.Resolver.ResolveEffectiveCarrierConfig(in.MCC, in.MNC)
	if err != nil {
		return nil, nil, fmt.Errorf("identity: 运营商解析失败: %w", err)
	}

	// 3. 身份三态
	ident := &Identity{
		MCC: in.MCC,
		MNC: in.MNC,
	}
	switch in.Mode {
	case ModeISIM:
		if in.IMPI == "" || in.IMPU == "" {
			return nil, nil, fmt.Errorf("identity: ISIM 模式需要 IMPI/IMPU")
		}
		ident.IMPI = in.IMPI
		ident.IMPU = in.IMPU
		ident.Mode = ModeISIM
	case ModeDerived:
		ident.IMPI = deriveIMPI(in.MCC, in.MNC)
		ident.IMPU = deriveIMPU(in.MCC, in.MNC)
		ident.Mode = ModeDerived
	case ModeAuto:
		if in.IMPI != "" && in.IMPU != "" {
			ident.IMPI = in.IMPI
			ident.IMPU = in.IMPU
			ident.Mode = ModeISIM
		} else {
			ident.IMPI = deriveIMPI(in.MCC, in.MNC)
			ident.IMPU = deriveIMPU(in.MCC, in.MNC)
			ident.Mode = ModeDerived
		}
	default:
		return nil, nil, fmt.Errorf("identity: 未知模式 %d", in.Mode)
	}

	log.Info("身份准备完成", "mode", ident.Mode.String(), "impu", ident.IMPU)
	return ident, cfg, nil
}

// deriveIMPI 推导 IMPI（简化：实际从 SIM/AKA 获取）。
func deriveIMPI(mcc, mnc string) string {
	return fmt.Sprintf("derived@mcc%s.mnc%s.3gppnetwork.org", mcc, mnc)
}

// deriveIMPU 推导 IMPU。
func deriveIMPU(mcc, mnc string) string {
	return fmt.Sprintf("sip:derived@mcc%s.mnc%s.3gppnetwork.org", mcc, mnc)
}
