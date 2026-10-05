// Package transport 提供基于上游 sipgo 事务机的 SIP 发送管线（WS-6，D-012）。
//
// 全面采用 emiago/sipgo 的 Client/Server 事务层；删除 vowifi-go 自研
// sipTransport/waiters/分帧。自定义连接经 ServeTCP 单连接 Listener 注入，
// net.Conn 包装器捕获 EOF，非 INVITE 15s deadline ctx。
// 开工前置验证：R4 ctx 取消事务清理语义、CRLF pong 交织处理。
package transport
