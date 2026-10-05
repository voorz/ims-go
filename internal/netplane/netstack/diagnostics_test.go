package netstack

import (
	"context"
	"errors"
	"testing"

	"github.com/voorz/ims-go/internal/netplane/ipsec3gpp"
)

type writeFailureEndpoint struct{ *testInnerEndpoint }

func (e *writeFailureEndpoint) WritePacket(context.Context, []byte) error {
	return errors.New("endpoint write failed")
}

func TestNetworkDiagnosticsReportsEndpointWriteFailure(t *testing.T) {
	n := newOriginalTestNetwork(t, &writeFailureEndpoint{newTestInnerEndpoint()})
	t.Cleanup(func() { _ = n.Close() })
	conn, err := n.DialContext(context.Background(), "udp", nil, "10.0.0.1:5060", DialOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.Write([]byte("test")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return n.bridge.Stats().OutboundWriteErrors == 1 })
	d := n.IMSNetworkDiagnostics()["bridge"].(PacketBridgeStats)
	if d.OutboundPackets != 0 || d.OutboundTransformErrors != 0 {
		t.Fatalf("failed endpoint write reported successful delivery: %+v", d)
	}
}

func TestNetworkDiagnosticsFollowsCurrentIPSecInstallation(t *testing.T) {
	n := newOriginalTestNetwork(t, newTestInnerEndpoint())
	t.Cleanup(func() { _ = n.Close() })
	cleanup, err := n.InstallIPSec3GPP(context.Background(), testIPSecPolicy())
	if err != nil {
		t.Fatal(err)
	}
	d := AdaptIMSNetwork(n).IMSNetworkDiagnostics()
	ipsec := d["ipsec"].(ipsec3gpp.TransportDiagnostics)
	if d["available"] != true || d["ipsec_installed"] != true || ipsec.FlowS.LocalPort == 0 {
		t.Fatalf("missing protected port diagnostics: %+v", d)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	removed := n.IMSNetworkDiagnostics()
	if removed["ipsec_installed"] != false || removed["ipsec"] != nil {
		t.Fatal("removed IPsec policy still reported as current")
	}
}
