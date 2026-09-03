package broker

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestForbiddenIP(t *testing.T) {
	bad := []string{"127.0.0.1", "::1", "10.1.2.3", "172.16.0.9", "172.25.0.3", "192.168.1.1", "169.254.169.254", "0.0.0.0", "224.0.0.1", "fe80::1", "fd00::1"}
	for _, s := range bad {
		if !forbiddenIP(net.ParseIP(s)) {
			t.Errorf("%s must be forbidden", s)
		}
	}
	good := []string{"1.1.1.1", "8.8.8.8", "34.172.10.216", "2606:4700:4700::1111"}
	for _, s := range good {
		if forbiddenIP(net.ParseIP(s)) {
			t.Errorf("%s must be allowed", s)
		}
	}
	if !forbiddenIP(nil) {
		t.Error("unparseable IP must be forbidden")
	}
}

func TestValidateRelayTarget(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "gopher://x", "ftp://x", "http://"} {
		u, _ := url.Parse(raw)
		if err := ValidateRelayTarget(u); err == nil {
			t.Errorf("%s must be rejected", raw)
		}
	}
	for _, raw := range []string{"http://provider.example:8080", "https://sandbox.0g.ai"} {
		u, _ := url.Parse(raw)
		if err := ValidateRelayTarget(u); err != nil {
			t.Errorf("%s must be accepted: %v", raw, err)
		}
	}
}

// The finding's core: the guarded transport must refuse to CONNECT to
// loopback/private/metadata targets — checked at dial time (post-DNS), so a
// hostname resolving to a private IP is refused too (anti-rebinding).
func TestGuardedTransport_RefusesLocalDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("guarded transport must never reach a loopback server")
	}))
	defer srv.Close()

	client := &http.Client{Transport: GuardedTransport()}
	resp, err := client.Get(srv.URL) // 127.0.0.1:<port>
	if err == nil {
		resp.Body.Close()
		t.Fatal("dial to loopback must fail")
	}
	if !strings.Contains(err.Error(), "not publicly routable") {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = context.Background()
}

// Loop marking: the relay proxy stamps RelayLoopHeader on outbound requests so
// a second pass through the relay is refused at the handler.
func TestGuardedRelayProxy_StampsLoopHeader(t *testing.T) {
	target, _ := url.Parse("http://provider.example")
	proxy := GuardedRelayProxy(target)
	req := httptest.NewRequest(http.MethodGet, "http://broker/proxy/x/api/info", nil)
	proxy.Director(req)
	if req.Header.Get(RelayLoopHeader) == "" {
		t.Fatal("outbound relay request must carry the loop header")
	}
}
