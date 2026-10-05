package swu

import (
	"context"
	"strings"
)

func (s *Session) selectEPDGTransportEndpoint(ctx context.Context, endpoint string) (string, error) {
	s.epdgCandidate = nil
	if s.cfg.EPDGCandidates == nil {
		return endpoint, nil
	}
	// Direct sockets already cycle their DNS candidates until a peer responds.
	// Keep that transport policy intact; SOCKS5 otherwise pins the first IP.
	if s.cfg.TransportFactory == nil && strings.TrimSpace(configuredProxyAddress(s.cfg)) == "" {
		return endpoint, nil
	}
	attempt, err := s.cfg.EPDGCandidates.selectCandidate(ctx, epdgCandidateScope{
		endpoint: endpoint, dnsServer: s.cfg.DNSServer, deviceID: s.cfg.DeviceID,
		imsi: s.cfg.IMSI, apn: strings.TrimSpace(s.cfg.APN), proxyAddr: configuredProxyAddress(s.cfg),
	})
	if err != nil {
		return "", err
	}
	s.epdgCandidate = attempt
	s.Logger.Info("IKE ePDG candidate selected", "epdg", attempt.address.String(),
		"candidates", attempt.count, "round", attempt.round)
	return attempt.address.String(), nil
}

func (s *Session) finishEPDGCandidate(cause error) {
	attempt := s.epdgCandidate
	if attempt == nil {
		return
	}
	s.epdgCandidate = nil
	// Attribute the response to the actual transport, not a re-resolved name.
	if !s.remoteIP.Equal(attempt.address.IP) {
		return
	}
	if s.cfg.EPDGCandidates.complete(attempt, cause) {
		s.Logger.Warn("IKE ePDG address allocation rejected; next runtime retry will reselect from DNS",
			"epdg", attempt.address.String(),
			"candidates", attempt.count, "round", attempt.round)
	}
}
