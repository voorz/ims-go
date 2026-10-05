// Package sms 实现短信能力（WS-8）。
//
// MO 发送/队列/重试、MT 接收/去重/确认、长短信分片重组、投递报告状态机、
// SMMA、二进制短信分类（WAP Push/OTA，H6）。PDU 编解码对象化，
// 消除 raw→对象→raw 三重往返。对外暴露 DeliveryStore 等存储接口契约。
package sms
