package ims

import "time"

// subBufferSize 是每个订阅者的独立缓冲大小。
const subBufferSize = 16

// maxDecisionRecords 是 Client 保留的决策记录上限，超出后丢弃最旧的。
const maxDecisionRecords = 512

func newDispatcher() *dispatcher {
	return &dispatcher{subs: make(map[*subscription]struct{})}
}

// subscribe 注册事件处理函数，返回取消订阅函数。
// 每个订阅者运行在独立 goroutine 中消费自己的缓冲队列。
func (d *dispatcher) subscribe(h EventHandler) (unsubscribe func()) {
	s := &subscription{ch: make(chan Event, subBufferSize), handler: h}
	d.mu.Lock()
	if d.subs == nil {
		d.subs = make(map[*subscription]struct{})
	}
	d.subs[s] = struct{}{}
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range s.ch {
			s.handler(e)
		}
	}()

	return func() {
		d.mu.Lock()
		if _, ok := d.subs[s]; !ok {
			d.mu.Unlock()
			return
		}
		delete(d.subs, s)
		d.mu.Unlock()
		close(s.ch)
		<-done
	}
}

// publish 向所有订阅者投递事件，永不阻塞调用方。
// 某个订阅者缓冲满时仅对其计数丢弃，不影响其他订阅者。
func (d *dispatcher) publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for s := range d.subs {
		select {
		case s.ch <- e:
		default:
			s.dropped.Add(1)
		}
	}
}

// shutdown 关闭分发器：取消所有订阅并等待其消费 goroutine 退出。
func (d *dispatcher) shutdown() {
	d.mu.Lock()
	subs := make([]*subscription, 0, len(d.subs))
	for s := range d.subs {
		subs = append(subs, s)
	}
	d.subs = make(map[*subscription]struct{})
	d.mu.Unlock()
	for _, s := range subs {
		close(s.ch)
	}
}
