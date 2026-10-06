package register

// Variants 返回初始 REGISTER 的变体矩阵（按试错顺序）。
//
// 5+1 变体：base + 4 种 fallback。EAP direct 模式由调用方在首位插入。
func Variants() []Variant {
	return []Variant{
		{
			Name:            "base",
			InitialAuth:     "",
			IncludePANI:     true,
			IncludeCellular: true,
		},
		{
			Name:            "aka_empty_uri_first",
			InitialAuth:     "aka_empty_uri_first",
			IncludePANI:     true,
			IncludeCellular: true,
		},
		{
			Name:            "aka_empty",
			InitialAuth:     "aka_empty",
			IncludePANI:     true,
			IncludeCellular: true,
		},
		{
			Name:            "aka_zero_response_uri_first",
			InitialAuth:     "aka_zero_response_uri_first",
			IncludePANI:     true,
			IncludeCellular: true,
		},
		{
			Name:            "none",
			InitialAuth:     "none",
			IncludePANI:     false,
			IncludeCellular: false,
		},
	}
}

// EAPDirectVariant 返回 EAP 直接认证变体（复用 SWu RES）。
// 调用方应在有 EAP RES 可用时将其插到变体矩阵首位。
func EAPDirectVariant() Variant {
	return Variant{
		Name:            "eap_direct",
		InitialAuth:     "eap_direct",
		IncludePANI:     true,
		IncludeCellular: true,
	}
}

// IsEAPDirect 报告是否为 EAP 直接认证变体。
func (v Variant) IsEAPDirect() bool {
	return v.InitialAuth == "eap_direct"
}
