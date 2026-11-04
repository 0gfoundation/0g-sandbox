package tee

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/0gfoundation/0g-sandbox/internal/tee/tapp_service"
	"github.com/ethereum/go-ethereum/common"
)

// AppIdentity is an application identity verified by the local tapp's pinned
// Scan. Signer is the stable application signer, never a registered node signer.
type AppIdentity struct {
	AppID        string
	Signer       common.Address
	TLSPins      []string
	RegisteredAt uint64
	ExpiresAt    time.Time
}

func ResolveAppIdentity(ctx context.Context, chainID uint64, registry, appID string) (*AppIdentity, error) {
	if os.Getenv("BACKEND_TAPP_SOCKET") == "" {
		return nil, fmt.Errorf("tee: BACKEND_TAPP_SOCKET is required for identity discovery")
	}
	conn, err := dialTapp()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	resp, err := tapp_service.NewTappServiceClient(conn).ResolveAppIdentity(ctx, &tapp_service.ResolveAppIdentityRequest{
		ChainId: chainID, RegistryAddress: registry, AppId: appID,
	})
	if err != nil {
		return nil, fmt.Errorf("tee: ResolveAppIdentity: %w", err)
	}
	return appIdentityFromResponse(resp, chainID, registry, appID, time.Now())
}

func appIdentityFromResponse(resp *tapp_service.ResolveAppIdentityResponse, chainID uint64, registry, appID string, now time.Time) (*AppIdentity, error) {
	if resp == nil || !resp.Success || resp.ChainId != chainID || resp.AppId != appID ||
		!common.IsHexAddress(registry) || !common.IsHexAddress(resp.RegistryAddress) ||
		common.HexToAddress(registry) != common.HexToAddress(resp.RegistryAddress) ||
		resp.RegisteredAt == 0 || len(resp.Nodes) == 0 {
		return nil, fmt.Errorf("tee: identity response does not match app/chain/registry/instance")
	}
	out := &AppIdentity{AppID: appID, RegisteredAt: resp.RegisteredAt}
	seen := make(map[string]bool)
	for _, node := range resp.Nodes {
		if node == nil || node.CheckedAt <= 0 || node.CheckedAt > now.Unix()+30 || node.ExpiresAt <= now.Unix() ||
			node.ExpiresAt > node.CheckedAt+300 || node.ImageEnv != "prod" {
			return nil, fmt.Errorf("tee: expired or non-production identity")
		}
		pin := strings.ToLower(strings.TrimPrefix(node.TlsSpkiSha256, "0x"))
		decoded, err := hex.DecodeString(pin)
		if err != nil || len(decoded) != 32 {
			return nil, fmt.Errorf("tee: invalid app TLS pin")
		}
		// TLS can be initialized before the service signer on a current standby.
		// Its observation must still be valid, but it contributes no service pin.
		if node.AppSigner == "" {
			continue
		}
		if !common.IsHexAddress(node.AppSigner) || common.HexToAddress(node.AppSigner) == (common.Address{}) {
			return nil, fmt.Errorf("tee: invalid stable app signer")
		}
		signer := common.HexToAddress(node.AppSigner)
		if out.Signer != (common.Address{}) && out.Signer != signer {
			return nil, fmt.Errorf("tee: current nodes disagree about the stable app signer")
		}
		out.Signer = signer
		if !seen[pin] {
			seen[pin] = true
			out.TLSPins = append(out.TLSPins, pin)
		}
		expires := time.Unix(node.ExpiresAt, 0)
		if out.ExpiresAt.IsZero() || expires.Before(out.ExpiresAt) {
			out.ExpiresAt = expires
		}
	}
	if out.Signer == (common.Address{}) {
		return nil, fmt.Errorf("tee: no current node has a ready stable app signer")
	}
	return out, nil
}

// AppTLSCertificate obtains the actual attested app TLS identity. The server
// terminates TLS with this certificate inside the same TEE as the application.
func AppTLSCertificate(ctx context.Context, appID string) (tls.Certificate, error) {
	if os.Getenv("BACKEND_TAPP_SOCKET") == "" {
		return tls.Certificate{}, fmt.Errorf("tee: BACKEND_TAPP_SOCKET is required for app TLS")
	}
	conn, err := dialTapp()
	if err != nil {
		return tls.Certificate{}, err
	}
	defer conn.Close()
	resp, err := tapp_service.NewTappServiceClient(conn).GetAppTlsCert(ctx, &tapp_service.GetAppTlsCertRequest{AppId: appID})
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("tee: GetAppTlsCert: %w", err)
	}
	if !resp.Success {
		return tls.Certificate{}, fmt.Errorf("tee: GetAppTlsCert failed")
	}
	return tls.X509KeyPair([]byte(resp.CertPem), []byte(resp.KeyPem))
}
