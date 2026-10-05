// Package identity 实现启动前协议准备（WS-13，H9）。
//
// PrepareStart：Profile 校验 → 运营商解析 → IMEI 四级推导 → 身份三态
// （isim/auto/derived）→ EPDG 选择。准备与启动一体，由 Client.Start 内聚调用。
package identity
