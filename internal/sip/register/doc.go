// Package register 实现 REGISTER 全流程（WS-5）。
//
// 初始/刷新/注销、Digest-AKA、sec-agree（494/Security-Verify）、GRUU、
// 绑定清理、registrar 选择与恢复（penalty store、503 后 P-CSCF 切换）。
// 全部使用 sipgo 对象，禁止 raw string 拼 SIP（红线#1）。
package register
