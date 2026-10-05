package netstack

// 从 restoration_test.go 提取的测试 helper（D-006：restoration 测试不搬运，
// 但 diagnostics_test.go / protected_udp_test.go / gvisor_network_test.go
// 依赖此 fake endpoint，保留为行为测试基础设施）。

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/voorz/ims-go/internal/netplane/ipsec3gpp"
	"github.com/voorz/ims-go/internal/swu"
)

type endpointResult struct {
	packet []byte
	err    error
}

type testInnerEndpoint struct {
	reads     chan endpointResult
	writes    chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

func newTestInnerEndpoint() *testInnerEndpoint {
	return &testInnerEndpoint{
		reads: make(chan endpointResult, 8), writes: make(chan []byte, 8), done: make(chan struct{}),
	}
}

func (e *testInnerEndpoint) ReadPacket(ctx context.Context) ([]byte, error) {
	select {
	case result := <-e.reads:
		return append([]byte(nil), result.packet...), result.err
	case <-e.done:
		return nil, errors.New("test endpoint closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (e *testInnerEndpoint) WritePacket(ctx context.Context, packet []byte) error {
	select {
	case e.writes <- append([]byte(nil), packet...):
		return nil
	case <-e.done:
		return errors.New("test endpoint closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *testInnerEndpoint) Snapshot() swu.InnerPacketSnapshot {
	return swu.InnerPacketSnapshot{}
}

func (e *testInnerEndpoint) Close() error {
	e.closeOnce.Do(func() { close(e.done) })
	return nil
}

func newOriginalTestNetwork(t *testing.T, endpoint swu.InnerPacketEndpoint) *Network {
	t.Helper()
	network, err := NewNetwork(
		context.Background(), net.IPv4(10, 0, 0, 2), nil, 32, 0, endpoint, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	return network
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func testIPSecPolicy() ipsec3gpp.Policy {
	return ipsec3gpp.Policy{
		LocalIP: net.IPv4(10, 0, 0, 2), RemoteIP: net.IPv4(10, 0, 0, 1),
		LocalClientPort: 41000, LocalServerPort: 41001,
		RemoteClientPort: 51000, RemoteServerPort: 51001,
		LocalClientSPI: 0x11111111, LocalServerSPI: 0x22222222,
		RemoteClientSPI: 0x33333333, RemoteServerSPI: 0x44444444,
		Authentication: ipsec3gpp.AuthHMACSHA196, Encryption: ipsec3gpp.EncryptionAES,
		Protocol: ipsec3gpp.ProtocolESP, Mode: ipsec3gpp.ModeTransport,
		CK: bytes.Repeat([]byte{0x11}, 16), IK: bytes.Repeat([]byte{0x22}, 16),
	}
}
