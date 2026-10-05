package netstack

import "github.com/voorz/ims-go/internal/netplane/ipsec3gpp"

// IMSNetworkDiagnostics describes each packet boundary independently. Outbound
// transform success is not proof of an endpoint write or a peer acknowledgement.
func (n *Network) IMSNetworkDiagnostics() map[string]any {
	if n == nil || n.bridge == nil {
		return map[string]any{"available": false}
	}
	result := map[string]any{
		"available": true, "bridge": n.bridge.Stats(),
		"context_done":    n.ctx.Err() != nil,
		"inner_endpoint":  n.bridge.endpoint.Snapshot(),
		"ipsec_installed": false,
	}
	n.bridge.mu.RLock()
	defer n.bridge.mu.RUnlock()
	result["ipsec_generation"] = n.bridge.ipsecGeneration
	result["previous_ipsec"] = n.bridge.previousIPSec
	if transport, ok := n.bridge.transform.(*ipsec3gpp.Transport); ok {
		result["ipsec_installed"] = true
		result["ipsec"] = transport.Diagnostics()
	}
	return result
}

func (a *IMSNetworkAdapter) IMSNetworkDiagnostics() map[string]any {
	return a.network.IMSNetworkDiagnostics()
}
