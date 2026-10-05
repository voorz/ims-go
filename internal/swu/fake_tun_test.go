package swu

import (
	"errors"
	"sync"
)

// fakeTUN 是测试用的 TUN 设备替身
// （原名 restoredTUN，定义于已按 D-006 排除的 restoration 测试文件中）。
type fakeTUN struct {
	mu     sync.Mutex
	name   string
	closes int
}

func (t *fakeTUN) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closes++
	return nil
}

func (t *fakeTUN) DeviceName() string               { return t.name }
func (t *fakeTUN) Read([]byte) (int, error)         { return 0, errors.New("test TUN is not running") }
func (t *fakeTUN) Write(packet []byte) (int, error) { return len(packet), nil }

func (t *fakeTUN) closeCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closes
}
