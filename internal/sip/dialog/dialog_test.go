package dialog

import (
	"sync"
	"testing"

	"github.com/emiago/sipgo/sip"
)

func TestRegistry(t *testing.T) {
	r := NewRegistry(nil)
	d := &Dialog{
		ID:        ID{CallID: "call-1", LocalTag: "ltag", RemoteTag: "rtag"},
		RemoteURI: sip.Uri{Host: "bob.example.com"},
		LocalURI:  sip.Uri{Host: "alice.example.com"},
	}
	r.Add(d)
	if r.Count() != 1 {
		t.Errorf("Count = %d", r.Count())
	}
	got, ok := r.Get(d.ID)
	if !ok || got != d {
		t.Error("Get 失败")
	}
	r.Remove(d.ID)
	if r.Count() != 0 {
		t.Error("Remove 失败")
	}
}

func TestCSeqAtomic(t *testing.T) {
	d := &Dialog{ID: ID{CallID: "c"}}
	var wg sync.WaitGroup
	seen := sync.Map{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n := d.NextCSeq()
			if _, loaded := seen.LoadOrStore(n, true); loaded {
				t.Errorf("CSeq 重复: %d", n)
			}
		}()
	}
	wg.Wait()
}

func TestInDialogRequest(t *testing.T) {
	d := &Dialog{
		ID:        ID{CallID: "call-1", LocalTag: "ltag1", RemoteTag: "rtag1"},
		RemoteURI: sip.Uri{User: "bob", Host: "example.com"},
		LocalURI:  sip.Uri{User: "alice", Host: "example.com"},
	}
	req := d.NewInDialogRequest(sip.BYE)
	if req.Method != sip.BYE {
		t.Errorf("Method = %s", req.Method)
	}
	if h := req.GetHeader("Call-ID"); h == nil || h.Value() != "call-1" {
		t.Error("Call-ID 错误")
	}
	// CSeq 应递增
	req2 := d.NewInDialogRequest(sip.BYE)
	c1 := req.GetHeader("CSeq").Value()
	c2 := req2.GetHeader("CSeq").Value()
	if c1 == c2 {
		t.Error("CSeq 未递增")
	}
}

func TestPRACK(t *testing.T) {
	d := &Dialog{
		ID:        ID{CallID: "call-1", LocalTag: "ltag1", RemoteTag: "rtag1"},
		RemoteURI: sip.Uri{Host: "example.com"},
		LocalURI:  sip.Uri{Host: "example.com"},
	}
	req := d.NewPRACK(1234)
	if req.Method != sip.PRACK {
		t.Errorf("Method = %s", req.Method)
	}
	if h := req.GetHeader("RAck"); h == nil {
		t.Error("缺少 RAck 头")
	}
}
