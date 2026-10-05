package netstack

import (
	"context"
	"errors"

	"github.com/voorz/ims-go/internal/netplane/ipsec3gpp"
)

func (n *Network) InstallIPSec3GPP(
	ctx context.Context,
	policy ipsec3gpp.Policy,
) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n == nil || n.bridge == nil {
		return nil, errors.New("netstack: packet bridge is not available")
	}
	transport, err := ipsec3gpp.NewTransport(policy)
	if err != nil {
		return nil, err
	}
	n.bridge.SetTransformer(transport)
	return func() error {
		// Remove only this installation: an earlier cleanup can race with a
		// replacement installed by the current IMS security association.
		n.bridge.mu.Lock()
		if n.bridge.transform == transport {
			n.bridge.rememberIPSecLocked()
			n.bridge.transform = nil
		}
		n.bridge.mu.Unlock()
		return nil
	}, nil
}
