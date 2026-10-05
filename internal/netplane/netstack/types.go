package netstack

import (
	"context"
	"net"
)

// DialOptions 控制在隧道网络上建立的连接。
//
// 从 vowifi-go internal/vowifi/imscore 收拢时的最小化表面（D-011）：
// 只保留 netstack 实际使用的字段。Timeout 与 KeepAlive 以纳秒为单位；
// TCPMSS 为正时覆盖端点 MSS。
type DialOptions struct {
	Timeout   int64
	KeepAlive int64
	TCPMSS    int
}

// IMSNetwork 是 IMS 栈使用的隧道网络表面。
//
// 从 vowifi-go internal/vowifi/imscore.IMSNetwork 收拢时的最小化表面（D-011）。
// IMSNetworkAdapter 实现此接口。
type IMSNetwork interface {
	LocalIP() net.IP
	HasLocalIP(ip net.IP) bool
	ResolveIP(ctx context.Context, host string) (net.IP, error)
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	DialTCPContext(ctx context.Context, local, remote *net.TCPAddr) (net.Conn, error)
	ListenTCP(addr *net.TCPAddr) (net.Listener, error)
	ListenPacket(network string, addr *net.UDPAddr) (net.PacketConn, error)
}
