package swu

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/voorz/ims-go/internal/swu/ikev2"
)

// EPDGResolver supplies the existing DNS policy without coupling recovery to IO.
type EPDGResolver func(context.Context, string, string) (*net.UDPAddr, []net.IP, error)

// EPDGCandidateStore retains address-rejection history across runtime reconnects.
// It does not schedule retries: the runtime must still wait after Notify 36.
type EPDGCandidateStore struct {
	resolve EPDGResolver
	mu      sync.Mutex
	scopes  map[epdgCandidateScope]*epdgCandidateRound
}

type epdgCandidateScope struct {
	endpoint  string
	dnsServer string
	deviceID  string
	imsi      string
	apn       string
	proxyAddr string
}

type epdgCandidateRound struct {
	rejected map[string]bool
	round    int
	sequence uint64
	pending  uint64
}

type epdgCandidateAttempt struct {
	scope    epdgCandidateScope
	address  net.UDPAddr
	sequence uint64
	round    int
	count    int
}

func NewEPDGCandidateStore(resolve EPDGResolver) *EPDGCandidateStore {
	return &EPDGCandidateStore{resolve: resolve, scopes: make(map[epdgCandidateScope]*epdgCandidateRound)}
}

func (s *EPDGCandidateStore) selectCandidate(ctx context.Context, scope epdgCandidateScope) (*epdgCandidateAttempt, error) {
	if s.resolve == nil {
		return nil, errors.New("swu: ePDG candidate resolver is required")
	}
	resolved, ips, err := s.resolve(ctx, scope.endpoint, scope.dnsServer)
	if err != nil {
		return nil, fmt.Errorf("swu: resolve ePDG candidates: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, errors.New("swu: ePDG resolver returned no endpoint")
	}
	addresses, err := orderedEPDGCandidates(ips)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.scopes[scope]
	if state == nil {
		state = &epdgCandidateRound{rejected: make(map[string]bool), round: 1}
		s.scopes[scope] = state
	}
	address := state.selectAddress(addresses)
	state.sequence++
	state.pending = state.sequence
	return &epdgCandidateAttempt{
		scope: scope, address: net.UDPAddr{IP: address, Port: resolved.Port},
		sequence: state.sequence, round: state.round, count: len(addresses),
	}, nil
}

func (s *epdgCandidateRound) selectAddress(addresses []net.IP) net.IP {
	for _, ip := range addresses {
		if !s.rejected[ip.String()] {
			return ip
		}
	}
	// All current DNS candidates rejected allocation. A later runtime retry
	// starts another round, rather than permanently excluding the entire pool.
	clear(s.rejected)
	s.round++
	return addresses[0]
}

// complete consumes only the owning attempt. A late overlapping candidate must
// not clear or modify the history of a newer attempt.
func (s *EPDGCandidateStore) complete(attempt *epdgCandidateAttempt, cause error) bool {
	if attempt == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.scopes[attempt.scope]
	if state == nil || state.pending != attempt.sequence {
		return false
	}
	state.pending = 0
	if cause == nil {
		clear(state.rejected)
		state.round = 1
		return false
	}
	var rejection *IKEAuthError
	if !errors.As(cause, &rejection) || rejection.NotifyType != ikev2.INTERNAL_ADDRESS_FAILURE {
		return false
	}
	state.rejected[attempt.address.IP.String()] = true
	return true
}

func orderedEPDGCandidates(ips []net.IP) ([]net.IP, error) {
	var ipv4, ipv6 []net.IP
	seen := make(map[string]bool)
	for _, ip := range ips {
		if ip.To16() == nil {
			return nil, errors.New("swu: ePDG resolver returned an invalid IP address")
		}
		if seen[ip.String()] {
			continue
		}
		seen[ip.String()] = true
		if v4 := ip.To4(); v4 != nil {
			ipv4 = append(ipv4, append(net.IP(nil), v4...))
		} else {
			ipv6 = append(ipv6, append(net.IP(nil), ip...))
		}
	}
	addresses := append(ipv4, ipv6...)
	if len(addresses) == 0 {
		return nil, errors.New("swu: ePDG resolver returned no IP addresses")
	}
	return addresses, nil
}
