package ims

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func collect(d *dispatcher, h EventHandler) (unsubscribe func()) {
	return d.subscribe(h)
}

func TestDispatcher_PublishSubscribe_Order(t *testing.T) {
	d := newDispatcher()
	var mu sync.Mutex
	var got []EventType
	unsub := collect(d, func(e Event) {
		mu.Lock()
		got = append(got, e.Type)
		mu.Unlock()
	})
	defer unsub()

	d.publish(Event{Type: EventClientStarted})
	d.publish(Event{Type: EventClientStopped})

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("2s 内只收到 %d 个事件", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got[0] != EventClientStarted || got[1] != EventClientStopped {
		t.Errorf("事件顺序错误: %v", got)
	}
}

func TestDispatcher_SlowConsumerIsolated(t *testing.T) {
	d := newDispatcher()

	release := make(chan struct{})
	// 慢消费者：处理函数阻塞在 release 上，直到测试结束
	unsubSlow := d.subscribe(func(e Event) {
		<-release
	})
	// 注意 defer 顺序（LIFO）：必须先 close(release) 放行慢消费者，
	// 再 unsubSlow() 等待其 goroutine 退出，否则死锁。
	defer unsubSlow()
	defer close(release)

	var fastCount atomic.Int64
	unsubFast := d.subscribe(func(e Event) {
		fastCount.Add(1)
	})
	defer unsubFast()

	// 核心属性：即使慢消费者卡死，publish 也永不阻塞。
	// 若分发器在慢消费者上阻塞，这 32 次发布将无法在 1s 内完成。
	const n = 32 // > subBufferSize(16)，慢消费者必然溢出
	start := time.Now()
	for i := 0; i < n; i++ {
		d.publish(Event{Type: EventModuleStarted})
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("publish 被慢消费者阻塞，32 次发布耗时 %v", elapsed)
	}

	// 慢消费者的溢出应被计数（隔离生效），快消费者不受影响（收到一部分）。
	var slowDropped int64
	d.mu.RLock()
	for s := range d.subs {
		slowDropped += s.dropped.Load()
	}
	d.mu.RUnlock()
	if slowDropped == 0 {
		t.Error("慢消费者应有丢弃计数（隔离生效），实际为 0")
	}
	// 快消费者的 goroutine 调度有延迟，轮询等待而非立即断言
	waitFor(t, 2*time.Second, "快消费者收到事件", func() bool {
		return fastCount.Load() > 0
	})
}

func TestDispatcher_Unsubscribe(t *testing.T) {
	d := newDispatcher()
	var count atomic.Int64
	unsub := d.subscribe(func(e Event) { count.Add(1) })

	d.publish(Event{Type: EventClientStarted})
	time.Sleep(50 * time.Millisecond)
	unsub()
	d.publish(Event{Type: EventClientStopped})
	time.Sleep(50 * time.Millisecond)

	if count.Load() != 1 {
		t.Errorf("取消订阅后不应再收到事件，实际收到 %d", count.Load())
	}
}
