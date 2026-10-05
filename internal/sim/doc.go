// Package sim 提供 AKA 认证能力（WS-2，D-015）。
//
// 包括：AKAProvider 契约（CalculateAKA）、Digest AKAv1-MD5（RFC 3310，
// 返回 response 值而非整头字符串，由 sip 侧用 sipgo 对象装配）、
// milenage 软 SIM（F1–F5*，默认关闭、显式 opt-in，仅 3GPP 测试钥）。
package sim
