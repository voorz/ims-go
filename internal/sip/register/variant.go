package register

// Variant 是初始 REGISTER 的一种"花样"。
//
// 生产洞察（vowifi-core）：IMS 注册失败 90% 是"请求格式不对"（换变体解决）
// 或"P-CSCF 挂了"（换台解决），只有 10% 需要等待重试。
// 变体矩阵按顺序试错，哪个成功用哪个。
type Variant struct {
	// Name 变体名，用于日志和决策记录。
	Name string
	// InitialAuth 初始 Authorization 模式：
	//   ""                        - 不带 Authorization 头（标准）
	//   "aka_empty"               - 空 Digest-AKA 占位
	//   "aka_empty_uri_first"     - 空 Digest-AKA，URI 优先
	//   "aka_zero_response_uri_first" - response=0 的 Digest-AKA，URI 优先
	//   "none"                    - 明确不带认证头
	//   "eap_direct"              - 复用 SWu 阶段 EAP-AKA 的 RES（防 SQN 双消耗）
	InitialAuth string
	// IncludePANI 是否带 P-Access-Network-Info 头。
	IncludePANI bool
	// IncludeCellular 是否带蜂窝网络信息。
	IncludeCellular bool
}

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
