// Package ims 是 ims-go 唯一对外公开的包（D-010）。
//
// 对外只暴露：New(Config) (*Client, error)。Client 一次构造、一直使用，
// 内部收回生命周期管理、期望态对账、恢复策略（H2/H3）、入站桥接（H1）、
// 启动前协议准备（H9）。实现细节全部位于 internal/，不得对外泄露。
//
// 本包在 WS-1 实现。
package ims
