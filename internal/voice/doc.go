// Package voice 实现语音呼叫引擎（WS-11，D-014）。
//
// per-device Agent + 显式 8 状态机（数据驱动 transitionMap）+ Actor
// 单 goroutine 串行 + IMS/LAN 双腿桥接。类型集中 types.go/runtime_types.go
// （红线#9）。入站桥接一等能力见子包 bridge（H1）。
package voice
