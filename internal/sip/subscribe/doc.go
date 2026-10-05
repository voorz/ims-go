// Package subscribe 实现 SUBSCRIBE/NOTIFY（WS-7）。
//
// SUBSCRIBE(reg)/MWI、dialog、lifecycle、NOTIFY 队列、reginfo 解析。
// 注册丢失时联动 register 包重建订阅。
package subscribe
