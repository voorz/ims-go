package ims

import (
	"sync"
	"sync/atomic"
)

// 本文件集中存放包内运行时类型（红线#9/P4）：未导出的实现细节类型。
// 对外类型一律在 types.go。

// lcState 是客户端生命周期的内部状态，与公开的 ClientState 一一对应。
// 状态流转只允许：stopped→starting→running→stopping→stopped，
// 由 Client.state 上的 CAS 保证并发安全（替代消费方侧的 epoch 机制）。
type lcState int32

const (
	lcStopped lcState = iota
	lcStarting
	lcRunning
	lcStopping
)

// public 映射到对外状态。
func (s lcState) public() ClientState {
	switch s {
	case lcStarting:
		return StateStarting
	case lcRunning:
		return StateRunning
	case lcStopping:
		return StateStopping
	default:
		return StateStopped
	}
}

// moduleSlot 是监督器跟踪的模块槽位：固定顺序启动/停止。
type moduleSlot struct {
	name   string
	module Module
}

// dispatcher 是单级事件分发器（H5）。
// 发布永不阻塞；每个订阅者拥有独立缓冲队列，慢消费者被隔离，
// 溢出时计数丢弃（sub.dropped），不影响其他订阅者。
type dispatcher struct {
	mu   sync.RWMutex
	subs map[*subscription]struct{}
}

// subscription 是一次订阅。
type subscription struct {
	ch      chan Event
	handler EventHandler
	dropped atomic.Int64
}
