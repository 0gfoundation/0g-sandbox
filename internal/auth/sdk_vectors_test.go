package auth

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSDKVectors verifies the TypeScript SDK's golden signing vectors
// (sdk/ts/test/vectors.json) against the server's actual EIP-191 recovery —
// the cross-language contract that TS-signed requests authenticate here.
func TestSDKVectors(t *testing.T) {
	path := filepath.Join("..", "..", "sdk", "ts", "test", "vectors.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vectors (regenerate with `npm run vectors` in sdk/ts): %v", err)
	}

	var vectors []struct {
		Name       string          `json:"name"`
		Address    string          `json:"address"`
		Action     string          `json:"action"`
		ResourceID string          `json:"resourceId"`
		Payload    json.RawMessage `json:"payload"`
		Nonce      string          `json:"nonce"`
		ExpiresAt  int64           `json:"expiresAt"`
		Message    string          `json:"message"`
		MessageB64 string          `json:"messageB64"`
		Signature  string          `json:"signature"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	if len(vectors) == 0 {
		t.Fatal("no vectors")
	}

	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			msgBytes, err := base64.StdEncoding.DecodeString(v.MessageB64)
			if err != nil {
				t.Fatalf("decode messageB64: %v", err)
			}
			if string(msgBytes) != v.Message {
				t.Fatalf("messageB64 does not decode to message field")
			}

			// The signed JSON must parse into the middleware's SignedRequest shape.
			var req SignedRequest
			if err := json.Unmarshal(msgBytes, &req); err != nil {
				t.Fatalf("unmarshal SignedRequest: %v", err)
			}
			if req.Action != v.Action || req.ResourceID != v.ResourceID ||
				req.Nonce != v.Nonce || req.ExpiresAt != v.ExpiresAt {
				t.Fatalf("SignedRequest fields mismatch: %+v", req)
			}

			// Signature must recover to the declared wallet — same code path
			// the middleware uses.
			sig, err := hex.DecodeString(strings.TrimPrefix(v.Signature, "0x"))
			if err != nil {
				t.Fatalf("decode signature hex: %v", err)
			}
			recovered, err := Recover(msgBytes, sig)
			if err != nil {
				t.Fatalf("Recover: %v", err)
			}
			if !strings.EqualFold(recovered.Hex(), v.Address) {
				t.Fatalf("recovered %s, want %s", recovered.Hex(), v.Address)
			}
		})
	}
}
