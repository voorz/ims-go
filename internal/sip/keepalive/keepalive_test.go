package keepalive

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
)

func TestKeeperSuccess(t *testing.T) {
	calls := 0
	k := New(Config{
		Target:   "127.0.0.1:5060",
		Interval: 50 * time.Millisecond,
		Sender: func(ctx context.Context, req *sip.Request) (*sip.Response, error) {
			calls++
			return nil, nil
		},
	})
	k.Start()
	time.Sleep(160 * time.Millisecond)
	k.Stop()
	if calls < 2 {
		t.Errorf("保活次数 = %d，期望 >= 2", calls)
	}
}

func TestKeeperFailureThreshold(t *testing.T) {
	failed := make(chan struct{}, 1)
	k := New(Config{
		Target:      "127.0.0.1:5060",
		Interval:    30 * time.Millisecond,
		MaxFailures: 2,
		Sender: func(ctx context.Context, req *sip.Request) (*sip.Response, error) {
			return nil, errors.New("timeout")
		},
		OnFailed: func() { failed <- struct{}{} },
	})
	k.Start()
	defer k.Stop()
	select {
	case <-failed:
		// 达到阈值，触发恢复
	case <-time.After(2 * time.Second):
		t.Error("未触发失败阈值回调")
	}
}
