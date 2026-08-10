// Package tee retrieves the TEE application signing key.
//
// In a real TDX environment the key is fetched via gRPC from the local
// tapp-daemon (tapp_service.TappService/GetAppSecretKey).
// Outside TDX the MOCK_APP_PRIVATE_KEY environment variable is used instead.
package tee

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/0gfoundation/0g-sandbox/internal/tee/tapp_service"
)

// AppKey holds the keys returned by the TEE.
type AppKey struct {
	// PrivateKeyHex is the Ethereum private key as a lowercase hex string
	// without the "0x" prefix.
	PrivateKeyHex string
	// EthAddressHex is the derived Ethereum address (checksummed hex, "0x…").
	EthAddressHex string
}

// cached result — protected by mu.
var (
	mu        sync.Mutex
	cachedKey *AppKey
)

// Get returns the application signing key.
//
// Decision tree (same as the TypeScript getAppKey):
//  1. MOCK_TEE env var set → use MOCK_APP_PRIVATE_KEY (panic if absent)
//  2. Otherwise → gRPC call to tapp-daemon at BACKEND_TAPP_IP:BACKEND_TAPP_PORT
//
// Result is cached after the first successful call; errors are NOT cached
// so the caller can retry after a transient failure.
func Get(ctx context.Context) (*AppKey, error) {
	mu.Lock()
	defer mu.Unlock()
	if cachedKey != nil {
		return cachedKey, nil
	}
	key, err := fetch(ctx)
	if err != nil {
		return nil, err
	}
	cachedKey = key
	return cachedKey, nil
}

func fetch(ctx context.Context) (*AppKey, error) {
	if os.Getenv("MOCK_TEE") != "" {
		return fetchMock()
	}
	return fetchGRPC(ctx)
}

// fetchMock returns the key from environment variables (development / CI).
func fetchMock() (*AppKey, error) {
	raw := os.Getenv("MOCK_APP_PRIVATE_KEY")
	if raw == "" {
		return nil, fmt.Errorf("tee: MOCK_TEE is set but MOCK_APP_PRIVATE_KEY is empty")
	}
	keyHex := strings.TrimPrefix(raw, "0x")
	if len(keyHex) != 64 {
		return nil, fmt.Errorf("tee: MOCK_APP_PRIVATE_KEY must be a 32-byte hex string (got %d chars)", len(keyHex))
	}
	addr := os.Getenv("MOCK_APP_ETH_ADDRESS")
	return &AppKey{PrivateKeyHex: keyHex, EthAddressHex: addr}, nil
}

// fetchGRPC calls the tapp-daemon to retrieve the signing key.
//
// Required env vars:
//
//	BACKEND_TAPP_SOCKET unix socket of the tapp-daemon (e.g. /run/tapp/tapp.sock).
//	                    When set it takes precedence, keeping the RPC off any TCP port.
//	BACKEND_TAPP_IP     host of the tapp-daemon  (default: 127.0.0.1; used if no socket)
//	BACKEND_TAPP_PORT   port of the tapp-daemon  (default: 50051; used if no socket)
//	BACKEND_APP_NAME    application identifier
func fetchGRPC(ctx context.Context) (*AppKey, error) {
	appID := os.Getenv("BACKEND_APP_NAME")
	target := tappTarget()

	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("tee: grpc dial %s: %w", target, err)
	}
	defer conn.Close()

	client := tapp_service.NewTappServiceClient(conn)
	resp, err := client.GetAppSecretKey(ctx, &tapp_service.GetAppSecretKeyRequest{
		AppId:   appID,
		KeyType: "ethereum",
		X25519:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("tee: GetAppSecretKey: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("tee: GetAppSecretKey failed: %s", resp.Message)
	}
	if len(resp.PrivateKey) == 0 {
		return nil, fmt.Errorf("tee: GetAppSecretKey returned empty private key")
	}

	// proto bytes fields arrive as raw bytes (not base64 — that was the JSON layer).
	privHex := hex.EncodeToString(resp.PrivateKey)
	ethAddr := "0x" + hex.EncodeToString(resp.EthAddress)

	return &AppKey{PrivateKeyHex: privHex, EthAddressHex: ethAddr}, nil
}

func envOrDefault(key, dflt string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return dflt
}

// tappTarget builds the gRPC dial target for the tapp-daemon. A unix socket
// (BACKEND_TAPP_SOCKET) takes precedence over TCP (BACKEND_TAPP_IP:PORT); Go's
// grpc has a built-in "unix" resolver, so "unix:///run/tapp/tapp.sock" dials
// the socket with no custom dialer.
func tappTarget() string {
	if sock := os.Getenv("BACKEND_TAPP_SOCKET"); sock != "" {
		return "unix://" + sock
	}
	host := envOrDefault("BACKEND_TAPP_IP", "127.0.0.1")
	port := envOrDefault("BACKEND_TAPP_PORT", "50051")
	return host + ":" + port
}
