package config

import (
	"strings"
	"testing"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	for key, value := range map[string]string{
		"DAYTONA_API_URL":     "http://daytona:3000",
		"DAYTONA_ADMIN_KEY":   "admin",
		"RPC_URL":             "http://rpc:8545",
		"SETTLEMENT_CONTRACT": "0x0000000000000000000000000000000000000001",
		"CHAIN_ID":            "16602",
	} {
		t.Setenv(key, value)
	}
}

func TestAttestorAppIDStartsWithWarnings(t *testing.T) {
	for _, tc := range []struct {
		name, keySource, tls string
		want                 []string
	}{
		{"kms_tls", "kms", "true", nil},
		{"kms_no_tls", "kms", "false", []string{"TLS_ENABLED=true"}},
		{"local_tls", "local", "true", []string{"PROVIDER_KEY_SOURCE=kms"}},
		{"local_no_tls", "local", "false", []string{"PROVIDER_KEY_SOURCE=kms", "TLS_ENABLED=true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("ATTESTOR_APP_ID", "attestor")
			t.Setenv("PROVIDER_KEY_SOURCE", tc.keySource)
			t.Setenv("TLS_ENABLED", tc.tls)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("startup refused: %v", err)
			}
			if cfg.Chain.AttestorAppID != "attestor" {
				t.Fatalf("ATTESTOR_APP_ID = %q", cfg.Chain.AttestorAppID)
			}
			warnings := cfg.Warnings()
			if len(warnings) != len(tc.want) {
				t.Fatalf("warnings %q", warnings)
			}
			for i, want := range tc.want {
				if !strings.Contains(warnings[i], want) {
					t.Fatalf("warning %q does not name %s", warnings[i], want)
				}
			}
		})
	}
}

func TestNoAttestorAppIDNoWarnings(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ATTESTOR_APP_ID", "")
	t.Setenv("PROVIDER_KEY_SOURCE", "local")
	t.Setenv("TLS_ENABLED", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if warnings := cfg.Warnings(); len(warnings) != 0 {
		t.Fatalf("warnings %q", warnings)
	}
}

func TestInvalidProviderKeySourceStillRefused(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("PROVIDER_KEY_SOURCE", "node")
	if _, err := Load(); err == nil {
		t.Fatal("accepted an unknown PROVIDER_KEY_SOURCE")
	}
}
