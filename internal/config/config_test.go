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

func TestAttestorInstanceNormalized(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ATTESTOR_APP_ID", "attestor")
	t.Setenv("ATTESTOR_INSTANCE", " 1784691493:0x9C58AbCdEf0123456789aBcDeF0123456789aBcD ")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if want := "1784691493:0x9c58abcdef0123456789abcdef0123456789abcd"; cfg.Chain.AttestorInstance != want {
		t.Fatalf("ATTESTOR_INSTANCE = %q, want %q", cfg.Chain.AttestorInstance, want)
	}
}

func TestInvalidAttestorInstanceRefused(t *testing.T) {
	for _, value := range []string{
		"1784691493",
		"0x9c58abcdef0123456789abcdef0123456789abcd",
		"0:0x9c58abcdef0123456789abcdef0123456789abcd",
		"-1:0x9c58abcdef0123456789abcdef0123456789abcd",
		"1784691493:9c58abcdef0123456789abcdef0123456789abcd",
		"1784691493:0x9c58",
		"1784691493:0x0000000000000000000000000000000000000000",
		"0x9c58abcdef0123456789abcdef0123456789abcd:1784691493",
	} {
		t.Run(value, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("ATTESTOR_APP_ID", "attestor")
			t.Setenv("ATTESTOR_INSTANCE", value)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ATTESTOR_INSTANCE") {
				t.Fatalf("accepted %q: %v", value, err)
			}
		})
	}
}

func TestAttestorInstanceWithoutAppIDWarns(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ATTESTOR_APP_ID", "")
	t.Setenv("ATTESTOR_INSTANCE", "1784691493:0x9c58abcdef0123456789abcdef0123456789abcd")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if warnings := cfg.Warnings(); len(warnings) != 1 || !strings.Contains(warnings[0], "ATTESTOR_APP_ID") {
		t.Fatalf("warnings %q", warnings)
	}
}
