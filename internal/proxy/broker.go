package proxy

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/auth"
)

// brokerClient calls the Broker's /api/session endpoints on behalf of the
// billing proxy, signing each request with the provider's TEE key.
type brokerClient struct {
	url                string
	providerKey        *ecdsa.PrivateKey // signs broker requests: the broker checks signer == provider
	providerAddr       string
	voucherIntervalSec int64
	httpClient         *http.Client
	log                *zap.Logger
}

func newBrokerClient(url string, providerKey *ecdsa.PrivateKey, providerAddr string, voucherIntervalSec int64, log *zap.Logger) *brokerClient {
	return &brokerClient{
		url:                url,
		providerKey:        providerKey,
		providerAddr:       providerAddr,
		voucherIntervalSec: voucherIntervalSec,
		httpClient:         &http.Client{Timeout: 3 * time.Minute},
		log:                log,
	}
}

type sessionPayload struct {
	SandboxID          string `json:"sandbox_id"`
	ProviderAddr       string `json:"provider_addr"`
	UserAddr           string `json:"user_addr"`
	CPU                int64  `json:"cpu"`
	MemGB              int64  `json:"mem_gb"`
	StartTime          int64  `json:"start_time"`
	VoucherIntervalSec int64  `json:"voucher_interval_sec"`
	Signature          string `json:"signature"`
}

// registerSession calls POST /api/session on the Broker.
// sandboxID may be "" for a funding-only call (Broker will fund but not write
// a monitoring session entry on its side).
func (b *brokerClient) registerSession(ctx context.Context, sandboxID, userAddr string, cpu, memGB int64) error {
	startTime := time.Now().Unix()

	msgHash := brokerSessionMsgHash(sandboxID, b.providerAddr, userAddr, cpu, memGB, startTime, b.voucherIntervalSec)
	sig, err := b.sign(msgHash)
	if err != nil {
		return fmt.Errorf("sign session: %w", err)
	}

	body, _ := json.Marshal(sessionPayload{
		SandboxID:          sandboxID,
		ProviderAddr:       b.providerAddr,
		UserAddr:           userAddr,
		CPU:                cpu,
		MemGB:              memGB,
		StartTime:          startTime,
		VoucherIntervalSec: b.voucherIntervalSec,
		Signature:          "0x" + hex.EncodeToString(sig),
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.url+"/api/session", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("broker POST /api/session: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode >= 300 {
		return fmt.Errorf("broker returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// deregisterSession calls DELETE /api/session/:id on the Broker.
// A 404 response is treated as success (session was never registered or already
// cleaned up), since the call is best-effort.
func (b *brokerClient) deregisterSession(ctx context.Context, sandboxID string) error {
	ts := time.Now().Unix()

	msgHash := brokerDeregisterMsgHash(sandboxID, ts)
	sig, err := b.sign(msgHash)
	if err != nil {
		return fmt.Errorf("sign deregister: %w", err)
	}

	body, _ := json.Marshal(struct {
		Timestamp int64  `json:"timestamp"`
		Signature string `json:"signature"`
	}{
		Timestamp: ts,
		Signature: "0x" + hex.EncodeToString(sig),
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		b.url+"/api/session/"+sandboxID, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("broker DELETE /api/session: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("broker returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// sign applies EIP-191 wrapping and signs msgHash with the TEE key.
func (b *brokerClient) sign(msgHash []byte) ([]byte, error) {
	prefixed := auth.HashMessage(msgHash)
	sig, err := crypto.Sign(prefixed, b.providerKey)
	if err != nil {
		return nil, err
	}
	sig[64] += 27 // normalize V to Ethereum convention (27/28)
	return sig, nil
}

// ── Message hash helpers (must mirror internal/broker/session.go) ─────────────

func brokerSessionMsgHash(sandboxID, providerAddr, userAddr string, cpu, memGB, startTime, voucherIntervalSec int64) []byte {
	return crypto.Keccak256(
		[]byte(sandboxID),
		common.HexToAddress(providerAddr).Bytes(),
		common.HexToAddress(userAddr).Bytes(),
		common.LeftPadBytes(big.NewInt(cpu).Bytes(), 8),
		common.LeftPadBytes(big.NewInt(memGB).Bytes(), 8),
		common.LeftPadBytes(big.NewInt(startTime).Bytes(), 8),
		common.LeftPadBytes(big.NewInt(voucherIntervalSec).Bytes(), 8),
	)
}

func brokerDeregisterMsgHash(sandboxID string, ts int64) []byte {
	return crypto.Keccak256(
		[]byte("deregister"),
		[]byte(sandboxID),
		common.LeftPadBytes(big.NewInt(ts).Bytes(), 8),
	)
}
