package broker

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// RelayLoopHeader marks a request as having already passed through the broker
// relay once. A provider URL pointing back at the relay (directly or through
// any chain of hosts) would otherwise recurse in-process until resource
// exhaustion — the header bounds the relay to a single hop.
const RelayLoopHeader = "X-0g-Broker-Relay"

// ValidateRelayTarget rejects provider URLs the relay must never dial.
// Provider URLs come VERBATIM from on-chain service records that any
// permissionlessly-registered app owner controls — the relay is an
// unauthenticated public endpoint, so an unfiltered URL is an SSRF primitive
// aimed at whatever network the broker sits in (cloud metadata included).
func ValidateRelayTarget(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("provider URL scheme %q not allowed", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("provider URL has no host")
	}
	return nil
}

// forbiddenIP reports whether the relay may not connect to ip: loopback
// (self-recursion, local admin planes), private ranges (the broker's own
// network), link-local (cloud metadata: 169.254.169.254), unspecified and
// multicast.
func forbiddenIP(ip net.IP) bool {
	return ip == nil ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// GuardedTransport returns a transport whose dialer verifies the CONNECTED
// address, after DNS resolution — validating the hostname's records up front
// would leave a rebinding window (resolve A, connect B). Control runs for
// every connection attempt, so every address the dialer tries is checked.
func GuardedTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("relay dial %q: %w", address, err)
			}
			if ip := net.ParseIP(host); forbiddenIP(ip) {
				return fmt.Errorf("relay destination %s is not publicly routable", host)
			}
			return nil
		},
	}
	return &http.Transport{
		DialContext:           dialer.DialContext,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          32,
		IdleConnTimeout:       60 * time.Second,
	}
}

// sharedGuardedTransport is built once: http.Transport is safe for concurrent
// use, and a per-request transport would defeat connection pooling and leak
// idle conns from discarded pools until GC.
var sharedGuardedTransport = GuardedTransport()

// GuardedRelayProxy builds the reverse proxy the broker relay uses: shared
// guarded transport + single-hop loop marking + a distinct error for refused
// destinations (an operator who registered a private/localhost provider URL
// gets told WHY instead of a generic upstream error).
func GuardedRelayProxy(target *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = sharedGuardedTransport
	orig := proxy.Director
	proxy.Director = func(req *http.Request) {
		orig(req)
		req.Header.Set(RelayLoopHeader, "1")
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		if strings.Contains(err.Error(), "not publicly routable") {
			http.Error(w, "provider URL is not publicly routable (private, loopback, or link-local address)", http.StatusBadGateway)
			return
		}
		http.Error(w, "upstream provider unreachable", http.StatusBadGateway)
	}
	return proxy
}
