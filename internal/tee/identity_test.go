package tee

import (
	"strings"
	"testing"
	"time"

	"github.com/0gfoundation/0g-sandbox/internal/tee/tapp_service"
)

func TestIdentityDiscoveryDoesNotSubstituteTheNodeSigner(t *testing.T) {
	now := time.Now()
	registry := "0x1111111111111111111111111111111111111111"
	app := "0x2222222222222222222222222222222222222222"
	node := "0x3333333333333333333333333333333333333333"
	fixture := func() *tapp_service.ResolveAppIdentityResponse {
		return &tapp_service.ResolveAppIdentityResponse{Success: true, ChainId: 16602, RegistryAddress: registry, AppId: "attestor", RegisteredAt: 100,
			Nodes: []*tapp_service.ResolvedAppNodeIdentity{{NodeSigner: node, AppSigner: app, TlsSpkiSha256: "0x" + strings.Repeat("11", 32), CheckedAt: now.Unix(), ExpiresAt: now.Unix() + 60, ImageEnv: "prod"}}}
	}
	identity, err := appIdentityFromResponse(fixture(), 16602, registry, "attestor", now)
	if err != nil || !strings.EqualFold(identity.Signer.Hex(), app) {
		t.Fatalf("wrong app signer: %+v %v", identity, err)
	}
	for name, mutate := range map[string]func(*tapp_service.ResolveAppIdentityResponse){
		"missing app signer": func(r *tapp_service.ResolveAppIdentityResponse) { r.Nodes[0].AppSigner = "" },
		"wrong scope":        func(r *tapp_service.ResolveAppIdentityResponse) { r.ChainId = 1 },
		"missing instance":   func(r *tapp_service.ResolveAppIdentityResponse) { r.RegisteredAt = 0 },
		"expired":            func(r *tapp_service.ResolveAppIdentityResponse) { r.Nodes[0].ExpiresAt = now.Unix() - 1 },
		"bad pin":            func(r *tapp_service.ResolveAppIdentityResponse) { r.Nodes[0].TlsSpkiSha256 = "01" },
		"dev image":          func(r *tapp_service.ResolveAppIdentityResponse) { r.Nodes[0].ImageEnv = "dev" },
		"conflicting app signer": func(r *tapp_service.ResolveAppIdentityResponse) {
			r.Nodes = append(r.Nodes, &tapp_service.ResolvedAppNodeIdentity{AppSigner: node, TlsSpkiSha256: r.Nodes[0].TlsSpkiSha256, CheckedAt: now.Unix(), ExpiresAt: now.Unix() + 60, ImageEnv: "prod"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := fixture()
			mutate(r)
			if _, err := appIdentityFromResponse(r, 16602, registry, "attestor", now); err == nil {
				t.Fatal("invalid identity accepted")
			}
		})
	}
}

func TestTLSOnlyStandbyDoesNotContributeServicePinsOrLease(t *testing.T) {
	now := time.Now()
	registry := "0x1111111111111111111111111111111111111111"
	signer := "0x2222222222222222222222222222222222222222"
	ready := &tapp_service.ResolvedAppNodeIdentity{AppSigner: signer, ImageEnv: "prod", TlsSpkiSha256: strings.Repeat("11", 32), CheckedAt: now.Unix(), ExpiresAt: now.Unix() + 120}
	standby := &tapp_service.ResolvedAppNodeIdentity{ImageEnv: "prod", TlsSpkiSha256: strings.Repeat("22", 32), CheckedAt: now.Unix() - 100, ExpiresAt: now.Unix() + 10}
	response := &tapp_service.ResolveAppIdentityResponse{Success: true, ChainId: 16602, RegistryAddress: registry, AppId: "attestor", RegisteredAt: 100,
		Nodes: []*tapp_service.ResolvedAppNodeIdentity{standby, ready}}
	identity, err := appIdentityFromResponse(response, 16602, registry, "attestor", now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(identity.Signer.Hex(), signer) || len(identity.TLSPins) != 1 || identity.TLSPins[0] != ready.TlsSpkiSha256 || identity.ExpiresAt.Unix() != ready.ExpiresAt {
		t.Fatalf("standby contributed a service pin or lease: %+v", identity)
	}
	response.Nodes = []*tapp_service.ResolvedAppNodeIdentity{standby}
	if _, err := appIdentityFromResponse(response, 16602, registry, "attestor", now); err == nil {
		t.Fatal("TLS alone authenticated a service signer")
	}

	for name, mutate := range map[string]func(*tapp_service.ResolvedAppNodeIdentity){
		"invalid signer":     func(n *tapp_service.ResolvedAppNodeIdentity) { n.AppSigner = "invalid" },
		"conflicting signer": func(n *tapp_service.ResolvedAppNodeIdentity) { n.AppSigner = registry },
		"expired":            func(n *tapp_service.ResolvedAppNodeIdentity) { n.ExpiresAt = now.Unix() - 1 },
		"bad pin":            func(n *tapp_service.ResolvedAppNodeIdentity) { n.TlsSpkiSha256 = "bad" },
		"dev image":          func(n *tapp_service.ResolvedAppNodeIdentity) { n.ImageEnv = "dev" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := *standby
			mutate(&invalid)
			response.Nodes = []*tapp_service.ResolvedAppNodeIdentity{ready, &invalid}
			if _, err := appIdentityFromResponse(response, 16602, registry, "attestor", now); err == nil {
				t.Fatal("standby hid an invalid or conflicting node")
			}
		})
	}
}
