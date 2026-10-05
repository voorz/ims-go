// Package swu 提供 SWu/IKEv2 隧道能力（WS-3，D-011）。
//
// 从 vowifi-go engine/swu 收拢而来，单仓库自维护：IKE_SA_INIT、
// IKE_AUTH/EAP-AKA、Child SA、双向重钥、重认证、MOBIKE、DPD、
// 恢复、redirect、分片、DELETE。netlink 统一使用 voorz/netlink。
package swu
