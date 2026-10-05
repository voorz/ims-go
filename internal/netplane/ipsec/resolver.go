package ipsec

import (
	"context"
	"net"
	"strings"

	vowifidns "github.com/voorz/ims-go/internal/dns"
)

// ResolveUDPAddrAll resolves the legacy host:port input and returns both the
// preferred endpoint and every distinct address for IKE failover.
func ResolveUDPAddrAll(addr, dnsServer string) (*net.UDPAddr, []net.IP, error) {
	return ResolveUDPAddrAllContext(context.Background(), addr, dnsServer)
}

// ResolveUDPAddrAllContext lets a canceled runtime stop staged DNS resolution.
func ResolveUDPAddrAllContext(ctx context.Context, addr, dnsServer string) (*net.UDPAddr, []net.IP, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	host, portName, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return nil, nil, err
	}
	port, err := net.LookupPort("udp", portName)
	if err != nil {
		return nil, nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		ip = ipv4Compat(ip)
		return &net.UDPAddr{IP: ip, Port: port}, []net.IP{ip}, nil
	}
	ips, err := vowifidns.LookupHostIPStaged(ctx, host, dnsServer)
	if err != nil {
		return nil, nil, err
	}
	preferred := preferIPv4(ips)
	return &net.UDPAddr{IP: preferred, Port: port}, ips, nil
}

func preferIPv4(ips []net.IP) net.IP {
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip
		}
	}
	return ips[0]
}

func ipv4Compat(ip net.IP) net.IP {
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip
}
