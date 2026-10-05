package xcap

import (
	"context"
	"net"
	"net/http"
	"testing"
)

func TestNewHTTPClientRequiresDialer(t *testing.T) {
	if NewHTTPClient(nil) != nil {
		t.Fatal("nil dialer must not build a client")
	}
	client := NewHTTPClient(func(context.Context, string, string) (net.Conn, error) {
		return nil, net.ErrClosed
	})
	if client == nil || client.Transport == nil {
		t.Fatal("dialer client missing transport")
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.ForceAttemptHTTP2 {
		t.Fatal("XCAP HTTP client must stay on HTTP/1.1")
	}
}
