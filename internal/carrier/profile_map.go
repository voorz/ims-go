package carrier

import (
	"strings"

	"github.com/voorz/ims-go/internal/carrier/profile"
)

// profileToConfig 将画像映射为 CarrierConfig。
// plmn 用于选择 plmn_overrides；mcc/mnc 用于填充标识。
func profileToConfig(p *profile.Profile, plmn, mcc, mnc string) *CarrierConfig {
	eff := p.Effective(plmn)
	out := &CarrierConfig{
		MCC:  mcc,
		MNC:  mnc,
		Name: p.ID,
		EPDG: eff.IMS.EPDG.Address,
	}
	out.IMSTemplate = IMSTemplate{
		UseDigestPlaceholder: false,
	}
	return out
}

// naiIdentifier 展开 local_identifier 模板为实际 NAI。
// 模板变量：$imsi、$mnc、$mcc。
func naiIdentifier(template, imsi, mcc, mnc string) string {
	s := strings.ReplaceAll(template, "$imsi", imsi)
	s = strings.ReplaceAll(s, "$mnc", mnc)
	s = strings.ReplaceAll(s, "$mcc", mcc)
	return s
}
