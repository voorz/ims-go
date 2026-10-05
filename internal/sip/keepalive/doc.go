// Package keepalive 提供 SIP 保活（WS-10）。
//
// OPTIONS ping、TCP CRLF pong、STUN 映射探测；失败达阈值触发恢复流程。
package keepalive
