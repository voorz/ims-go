package emergency

// Policy 是紧急呼叫策略（默认禁用）。
type Policy struct {
	// Enabled 显式 opt-in 后为 true。
	Enabled bool
}
