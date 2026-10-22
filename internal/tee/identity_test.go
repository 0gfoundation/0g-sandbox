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
			Nodes: []*tapp_service.ResolvedAppNodeIdentity{{NodeSigner: node, AppSigner: app, TlsSpkiSha256: "0x" + strings.Repeat("11", 32), CheckedAt: now.Unix(), ExpiresAt: now.Unix() + 60}}}
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
		"conflicting app signer": func(r *tapp_service.ResolveAppIdentityResponse) {
			r.Nodes = append(r.Nodes, &tapp_service.ResolvedAppNodeIdentity{AppSigner: node, TlsSpkiSha256: r.Nodes[0].TlsSpkiSha256, CheckedAt: now.Unix(), ExpiresAt: now.Unix() + 60})
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
