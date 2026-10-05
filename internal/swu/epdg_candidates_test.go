package swu

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/voorz/ims-go/internal/swu/ikev2"
)

func candidateResolver(addresses ...string) EPDGResolver {
	return func(ctx context.Context, endpoint, _ string) (*net.UDPAddr, []net.IP, error) {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		ips := make([]net.IP, len(addresses))
		for i, address := range addresses {
			ips[i] = net.ParseIP(address)
		}
		port := 500
		if endpoint != "" {
			_, service, err := net.SplitHostPort(endpoint)
			if err != nil {
				return nil, nil, err
			}
			port, err = net.LookupPort("udp", service)
			if err != nil {
				return nil, nil, err
			}
		}
		return &net.UDPAddr{Port: port}, ips, nil
	}
}

func selectCandidate(t *testing.T, store *EPDGCandidateStore, scope epdgCandidateScope) *epdgCandidateAttempt {
	t.Helper()
	attempt, err := store.selectCandidate(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

func addressRejection() error {
	return errors.Join(&IKEAuthError{NotifyType: ikev2.INTERNAL_ADDRESS_FAILURE}, context.DeadlineExceeded)
}

func TestEPDGCandidatesRotateAfterAddressRejectionAcrossSessions(t *testing.T) {
	store := NewEPDGCandidateStore(candidateResolver("2001:db8::1", "192.0.2.1", "192.0.2.2", "192.0.2.1"))
	for index, want := range []string{"192.0.2.1", "192.0.2.2", "2001:db8::1", "192.0.2.1"} {
		config := &Config{EPDGAddr: "epdg.example", EPDGCandidates: store, APN: "ims"}
		transport := newTestIKETransport()
		config.TransportFactory = func(_, remote string) (Transport, error) {
			host, _, err := net.SplitHostPort(remote)
			if err != nil || host != want {
				t.Fatalf("attempt %d target=%s, want %s: %v", index, remote, want, err)
			}
			transport.remoteIP = net.ParseIP(host)
			return transport, nil
		}
		session := NewSession(config)
		if err := session.buildTransport(context.Background()); err != nil {
			t.Fatal(err)
		}
		if session.epdgCandidate.count != 3 || session.epdgCandidate.round != index/3+1 {
			t.Fatalf("candidate metadata=%+v", session.epdgCandidate)
		}
		session.finishEPDGCandidate(addressRejection())
		session.stopTransport()
		if config.EPDGAddr != "epdg.example" || config.APN != "ims" {
			t.Fatal("transport selection mutated the configured identity")
		}
	}
}

func TestEPDGCandidatesUseFreshDNSWithoutResurrectingRejectedAddress(t *testing.T) {
	answers := [][]string{{"192.0.2.1", "192.0.2.2"}, {"192.0.2.2", "192.0.2.1", "192.0.2.3"}, {"192.0.2.3", "192.0.2.1"}}
	queries := 0
	store := NewEPDGCandidateStore(func(ctx context.Context, addr, dns string) (*net.UDPAddr, []net.IP, error) {
		resolver := candidateResolver(answers[queries]...)
		queries++
		return resolver(ctx, addr, dns)
	})
	for _, want := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		attempt := selectCandidate(t, store, epdgCandidateScope{endpoint: "epdg.example:500"})
		if attempt.address.IP.String() != want {
			t.Fatalf("selected %s, want %s", attempt.address.String(), want)
		}
		store.complete(attempt, addressRejection())
	}
	if queries != 3 {
		t.Fatalf("DNS lookups=%d, want a fresh lookup each attempt", queries)
	}
}

func TestEPDGCandidatesIgnoreOtherFailuresAndClearOnSuccess(t *testing.T) {
	store := NewEPDGCandidateStore(candidateResolver("192.0.2.1", "192.0.2.2"))
	scope := epdgCandidateScope{endpoint: "epdg.example:500"}
	store.complete(selectCandidate(t, store, scope), addressRejection())
	for _, failure := range []error{
		context.Canceled, context.DeadlineExceeded, errors.New("EOF"), errors.New("SIP 488"),
		&IKEAuthError{NotifyType: ikev2.AUTHENTICATION_FAILED},
	} {
		attempt := selectCandidate(t, store, scope)
		if attempt.address.IP.String() != "192.0.2.2" {
			t.Fatalf("unrelated failure changed target: %s", attempt.address.String())
		}
		store.complete(attempt, failure)
	}
	store.complete(selectCandidate(t, store, scope), nil)
	if got := selectCandidate(t, store, scope).address.IP.String(); got != "192.0.2.1" {
		t.Fatalf("successful establishment did not clear rejection history: %s", got)
	}
}

func TestEPDGCandidatesIsolateScopesAndIgnoreSupersededCompletion(t *testing.T) {
	store := NewEPDGCandidateStore(candidateResolver("192.0.2.1", "192.0.2.2"))
	main := epdgCandidateScope{endpoint: "epdg.example:500", apn: "ims", imsi: "test-subscriber"}
	old := selectCandidate(t, store, main)
	current := selectCandidate(t, store, main)
	store.complete(current, addressRejection())
	store.complete(old, nil)
	if got := selectCandidate(t, store, main).address.IP.String(); got != "192.0.2.2" {
		t.Fatalf("old success cleared current rejection: %s", got)
	}
	store.complete(current, addressRejection()) // A completion is consumed once.
	for _, change := range []func(*epdgCandidateScope){
		func(s *epdgCandidateScope) { s.apn = "xcap" },
		func(s *epdgCandidateScope) { s.endpoint = "redirect.example:500" },
		func(s *epdgCandidateScope) { s.imsi = "different-subscriber" },
		func(s *epdgCandidateScope) { s.proxyAddr = "different-proxy:1080" },
	} {
		scope := main
		change(&scope)
		if got := selectCandidate(t, store, scope).address.IP.String(); got != "192.0.2.1" {
			t.Fatalf("unrelated scope inherited rejection: %s", got)
		}
	}
}

func TestEPDGCandidatesSingleAddressStartsNewRound(t *testing.T) {
	store := NewEPDGCandidateStore(candidateResolver("192.0.2.1"))
	for round := 1; round <= 3; round++ {
		attempt := selectCandidate(t, store, epdgCandidateScope{})
		if attempt.round != round || attempt.address.IP.String() != "192.0.2.1" {
			t.Fatalf("single candidate became unavailable: %+v", attempt)
		}
		store.complete(attempt, addressRejection())
	}
}

func TestEPDGCandidatesConcurrentAttemptsDoNotCorruptOwnership(t *testing.T) {
	store := NewEPDGCandidateStore(candidateResolver("192.0.2.1", "192.0.2.2"))
	var pending sync.WaitGroup
	for range 20 {
		pending.Go(func() {
			attempt, err := store.selectCandidate(context.Background(), epdgCandidateScope{})
			if err != nil {
				t.Error(err)
				return
			}
			store.complete(attempt, addressRejection())
		})
	}
	pending.Wait()
	old := selectCandidate(t, store, epdgCandidateScope{})
	current := selectCandidate(t, store, epdgCandidateScope{})
	store.complete(current, nil)
	if store.complete(old, addressRejection()) {
		t.Fatal("superseded failure changed a successful successor's history")
	}
	if got := selectCandidate(t, store, epdgCandidateScope{}).address.IP.String(); got != "192.0.2.1" {
		t.Fatalf("stale completion resurrected rejection after success: %s", got)
	}
}

func TestEPDGCandidatesExposeResolutionFailureAndCancellation(t *testing.T) {
	dnsErr := errors.New("DNS unavailable")
	store := NewEPDGCandidateStore(func(context.Context, string, string) (*net.UDPAddr, []net.IP, error) {
		return nil, nil, dnsErr
	})
	if _, err := store.selectCandidate(context.Background(), epdgCandidateScope{}); !errors.Is(err, dnsErr) {
		t.Fatalf("DNS error hidden: %v", err)
	}
	store = NewEPDGCandidateStore(candidateResolver("192.0.2.1"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.selectCandidate(ctx, epdgCandidateScope{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lookup accepted: %v", err)
	}
	store = NewEPDGCandidateStore(candidateResolver())
	if _, err := store.selectCandidate(context.Background(), epdgCandidateScope{}); err == nil {
		t.Fatal("empty DNS answer accepted")
	}
}
