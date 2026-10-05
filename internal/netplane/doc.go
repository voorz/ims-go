// Package netplane 提供隧道内 IP 数据面（WS-4，D-013）。
//
// 默认 userspace 模式（进程内 ESP 加解密泵，免特权）+ gVisor 用户态
// 双栈（DNS 只走 ePDG 下发，防泄露）；tun/xfrmi 为显式 opt-in。
// 含 3GPP ESP 传输模式变换器与 ePDG 多 slot 会话管理。
package netplane
