// Package inbound 提供入站 SIP 分发（WS-10）。
//
// INVITE/BYE/CANCEL/OPTIONS/NOTIFY/MESSAGE 路由；单级事件分发。
// 入站响应一律用 sip.NewResponseFromRequest 构造（红线#1）。
package inbound
