package swu

import (
	"fmt"

	"github.com/voorz/ims-go/internal/swu/ikev2"
)

// IKEAuthError preserves an IKE_AUTH error Notify for protocol-specific recovery.
type IKEAuthError struct {
	NotifyType uint16
	// Only a post-EAP configuration rejection can leave this candidate IKE SA
	// established without a CHILD_SA (RFC 7296 sections 2.21.2 and 3.15.4).
	deleteCandidateIKE bool
}

func (e *IKEAuthError) Error() string {
	return fmt.Sprintf("swu: IKE_AUTH rejected with %s (%d)", ikev2.NotifyTypeToString(e.NotifyType), e.NotifyType)
}
