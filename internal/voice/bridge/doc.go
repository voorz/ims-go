// Package bridge 实现入站呼叫桥接一等能力（WS-11，H1）。
//
// 把入站 IMS 呼叫桥接到本地 SIP 端点：dialog 管理 + 媒体桥接 + 释放。
// 替代消费方手写的 421 行 Sprintf B2BUA（W1）。
package bridge
