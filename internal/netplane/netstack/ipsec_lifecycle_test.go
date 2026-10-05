package netstack

import (
	"context"
	"testing"

	"github.com/voorz/ims-go/internal/netplane/ipsec3gpp"
)

func TestRetiredIPSecCleanupCannotRemoveReplacement(t *testing.T) {
	network := newOriginalTestNetwork(t, newTestInnerEndpoint())
	t.Cleanup(func() { _ = network.Close() })
	oldCleanup, err := network.InstallIPSec3GPP(context.Background(), testIPSecPolicy())
	if err != nil {
		t.Fatal(err)
	}
	newCleanup, err := network.InstallIPSec3GPP(context.Background(), testIPSecPolicy())
	if err != nil {
		t.Fatal(err)
	}
	current := network.bridge.currentTransformer()
	if err := oldCleanup(); err != nil {
		t.Fatal(err)
	}
	if network.bridge.currentTransformer() != current || !network.IPSec3GPPPolicyInstalled() {
		t.Fatal("retired cleanup removed the replacement IPsec policy")
	}
	if err := newCleanup(); err != nil {
		t.Fatal(err)
	}
	if network.bridge.currentTransformer() != nil || network.IPSec3GPPPolicyInstalled() {
		t.Fatal("current cleanup retained the policy")
	}
}

func TestIPSecReplacementRetainsPreviousIdentifiersAndInvalidInstallKeepsCurrent(t *testing.T) {
	network := newOriginalTestNetwork(t, newTestInnerEndpoint())
	t.Cleanup(func() { _ = network.Close() })
	adapter := AdaptIMSNetwork(network)
	first, err := ipsec3gpp.NewPolicy(testIPSecPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.InstallIPSec3GPP(first); err != nil {
		t.Fatal(err)
	}
	original := network.bridge.currentTransformer()
	invalid := testIPSecPolicy()
	invalid.LocalIP = nil
	if err := adapter.InstallIPSec3GPP(invalid); err == nil {
		t.Fatal("invalid policy installed")
	}
	if network.bridge.currentTransformer() != original {
		t.Fatal("failed install removed healthy SA")
	}
	second, err := ipsec3gpp.NewPolicy(testIPSecPolicy())
	if err != nil {
		t.Fatal(err)
	}
	second.FlowS.InboundSPI++
	if err := adapter.InstallIPSec3GPP(second); err != nil {
		t.Fatal(err)
	}
	d := network.IMSNetworkDiagnostics()
	previous := d["previous_ipsec"].(*ipsec3gpp.TransportDiagnostics)
	current := d["ipsec"].(ipsec3gpp.TransportDiagnostics)
	if previous.FlowS.InboundSPI != first.FlowS.InboundSPI || current.FlowS.InboundSPI != second.FlowS.InboundSPI || d["ipsec_generation"] != uint64(2) {
		t.Fatalf("SA generations lost: %+v", d)
	}
}
