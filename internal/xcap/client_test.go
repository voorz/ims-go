package xcap

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestWrapUnavailableHidesRequestURL(t *testing.T) {
	err := wrapUnavailable(errors.New(`Get "https://xcap.example/users/sip:234159612768972@ims.example/simservs": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "234159612768972") || strings.Contains(err.Error(), "https://") {
		t.Fatalf("leaked %q", err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
}

func TestGetFallsBackToSecondXUIOn404(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		if strings.Contains(r.URL.Path, "sip:user") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write([]byte(`<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><originating-identity-presentation-restriction active="true"><default-behaviour>presentation-restricted</default-behaviour></originating-identity-presentation-restriction></simservs>`))
	}))
	defer server.Close()
	client := &Client{HTTP: server.Client(), Host: strings.TrimPrefix(server.URL, "http://")}
	client.HTTP.Transport = rewriteHTTPS(server)
	doc, err := client.Get(context.Background(), "sip:user@ims.example", []string{"tel:+15551234567"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !doc.IdentityRestricted() || doc.ETag != "v1" {
		t.Fatalf("doc = %+v", doc)
	}
	if len(seen) != 2 {
		t.Fatalf("requests = %v", seen)
	}
}

func TestPutRequiresIfMatchAndWritesOneDocument(t *testing.T) {
	var gotMatch, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("method %s", r.Method)
		}
		gotMatch = r.Header.Get("If-Match")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("ETag", `"v2"`)
		w.Write(body)
	}))
	defer server.Close()
	client := &Client{HTTP: server.Client(), Host: strings.TrimPrefix(server.URL, "http://")}
	client.HTTP.Transport = rewriteHTTPS(server)
	doc := Document{XUI: "sip:user@ims.example", ETag: "v1"}
	doc.SetCFU(true, "tel:+441234")
	got, err := client.Put(context.Background(), doc)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if gotMatch != `"v1"` || !strings.Contains(gotBody, "tel:+441234") {
		t.Fatalf("If-Match=%q body=%s", gotMatch, gotBody)
	}
	if got.ETag != "v2" || got.CFUTarget() != "tel:+441234" {
		t.Fatalf("result = %+v", got)
	}
	if _, err := client.Put(context.Background(), Document{XUI: "sip:user@ims.example"}); !strings.Contains(err.Error(), "If-Match") {
		t.Fatalf("missing etag err = %v", err)
	}
}

func TestPutMapsPreconditionFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
	}))
	defer server.Close()
	client := &Client{HTTP: server.Client(), Host: strings.TrimPrefix(server.URL, "http://")}
	client.HTTP.Transport = rewriteHTTPS(server)
	_, err := client.Put(context.Background(), Document{XUI: "sip:user@ims.example", ETag: "old"})
	if !errors.Is(err, ErrPrecondition) {
		t.Fatalf("err = %v", err)
	}
}

func TestRootURIUsesPub3GPPHostFromIMSDomain(t *testing.T) {
	xui := "sip:234159612768972@ims.mnc015.mcc234.3gppnetwork.org"
	got := RootURI("", "ims.mnc015.mcc234.3gppnetwork.org", xui)
	want := "https://xcap.ims.mnc015.mcc234.pub.3gppnetwork.org/" + simservsAUID + "/users/" + url.PathEscape(xui) + "/simservs"
	if got != want {
		t.Fatalf("RootURI = %q, want %q", got, want)
	}
	if got := xcapHostFromDomain("ims.example"); got != "xcap.ims.example" {
		t.Fatalf("operator domain host = %q", got)
	}
	if got := RootURI("xcap.configured.example", "ims.mnc015.mcc234.3gppnetwork.org", "sip:user@ims.example"); !strings.Contains(got, "https://xcap.configured.example/") {
		t.Fatalf("configured host lost: %q", got)
	}
}

func TestRootURIUsesOnNetHostFromIMSDomain(t *testing.T) {
	xui := "sip:user@ims.mnc015.mcc234.3gppnetwork.org"
	got := rootURI("", "ims.mnc015.mcc234.3gppnetwork.org", xui, true)
	want := "https://xcap.ims.mnc015.mcc234.3gppnetwork.org/" + simservsAUID + "/users/" + url.PathEscape(xui) + "/simservs"
	if got != want {
		t.Fatalf("on-net RootURI = %q, want %q", got, want)
	}
	if got := xcapOnNetHostFromDomain("ims.mnc015.mcc234.3gppnetwork.org"); got != "xcap.ims.mnc015.mcc234.3gppnetwork.org" {
		t.Fatalf("on-net host = %q", got)
	}
}

func rewriteHTTPS(server *httptest.Server) http.RoundTripper {
	base := server.Client().Transport
	if base == nil {
		base = http.DefaultTransport
	}
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.URL.Scheme = "http"
		clone.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return base.RoundTrip(clone)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
