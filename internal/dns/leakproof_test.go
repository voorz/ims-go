package dns

import (
	"context"
	"net"
	"testing"

	miekgdns "github.com/miekg/dns"
)

// TestDNSOnlyUsesEPDGServers 是 D-013 的防泄露断言：
// DNS 查询只允许发往 ePDG 下发的服务器，绝不能回落到系统默认 DNS。
// lookupHostIPViaDNSServers 的 server 列表是唯一的查询目标——
// 工厂只收到该列表中的地址即证明无泄露。
func TestDNSOnlyUsesEPDGServers(t *testing.T) {
	epdgServers := []net.IP{net.ParseIP("10.0.0.53"), net.ParseIP("2001:db8::53")}
	systemServer := net.ParseIP("8.8.8.8") // 绝不能被查询

	var queried []string
	factory := func(bindIP, server net.IP) queryResolver {
		queried = append(queried, server.String())
		return &recordingResolver{server: server}
	}

	_ = lookupHostIPViaDNSServers(
		context.Background(), "epdg.epc.mnc001.mcc001.pub.3gppnetwork.org",
		false, net.ParseIP("10.0.0.2"), epdgServers, factory,
	)

	if len(queried) == 0 {
		t.Fatal("工厂未被调用，测试无效")
	}
	allowed := map[string]bool{}
	for _, s := range epdgServers {
		allowed[s.String()] = true
	}
	for _, q := range queried {
		if q == systemServer.String() {
			t.Fatalf("DNS 泄露：查询了系统 DNS %s", q)
		}
		if !allowed[q] {
			t.Fatalf("DNS 查询了非 ePDG 下发的服务器 %s", q)
		}
	}
}

// TestDNSEmptyServersResolvesNothing 断言：无 ePDG DNS 服务器时不做任何查询
// （而不是回落到系统解析器）。
func TestDNSEmptyServersResolvesNothing(t *testing.T) {
	called := false
	factory := func(bindIP, server net.IP) queryResolver {
		called = true
		return &recordingResolver{server: server}
	}
	got := lookupHostIPViaDNSServers(
		context.Background(), "example.com", false, nil, nil, factory,
	)
	if called {
		t.Error("服务器列表为空时不应发起任何 DNS 查询")
	}
	if len(got) != 0 {
		t.Errorf("服务器列表为空时应返回空，实际 %v", got)
	}
}

type recordingResolver struct {
	server net.IP
}

func (r *recordingResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return []net.IPAddr{{IP: net.ParseIP("192.0.2.10")}}, nil
}

func (r *recordingResolver) LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error) {
	return "", nil, nil
}

func (r *recordingResolver) LookupNAPTR(ctx context.Context, name string) ([]*miekgdns.NAPTR, error) {
	return nil, nil
}
