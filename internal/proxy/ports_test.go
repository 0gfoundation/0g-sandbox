package proxy

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestValidatePublicPorts(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		sealed  bool
		wantErr string // empty = no error
	}{
		{"absent", `{"snapshot":"x"}`, false, ""},
		{"null", `{"publicPorts":null}`, false, ""},
		{"valid", `{"publicPorts":[8080,3000]}`, false, ""},
		{"empty array", `{"publicPorts":[]}`, false, "must not be empty"},
		{"not an array", `{"publicPorts":"8080"}`, false, "array of integers"},
		{"float element", `{"publicPorts":[8080.5]}`, false, "array of integers"},
		{"string element", `{"publicPorts":["8080"]}`, false, "array of integers"},
		{"out of range", `{"publicPorts":[70000]}`, false, "out of range"},
		{"zero", `{"publicPorts":[0]}`, false, "out of range"},
		{"reserved terminal", `{"publicPorts":[22222]}`, false, "reserved"},
		{"reserved toolbox", `{"publicPorts":[2280]}`, false, "reserved"},
		{"reserved recording", `{"publicPorts":[33333]}`, false, "reserved"},
		{"too many", `{"publicPorts":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17]}`, false, "at most"},
		{"sealed with 8080", `{"publicPorts":[8080]}`, true, ""},
		{"sealed without 8080", `{"publicPorts":[3000]}`, true, "must include port 8080"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePublicPorts([]byte(c.body), c.sealed)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, c.wantErr)
			}
		})
	}
}

func TestDecoratePublicPorts_Supported(t *testing.T) {
	os.Setenv("PROXY_DOMAIN", "1.2.3.4.nip.io:4000")
	defer os.Unsetenv("PROXY_DOMAIN")

	resp := `{"id":"abc-123","publicPorts":[8080,3000]}`
	out, supported, err := decoratePublicPorts([]byte(resp))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !supported {
		t.Fatal("response with publicPorts must report supported")
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	urls, ok := m["preview_urls"].(map[string]any)
	if !ok {
		t.Fatalf("preview_urls missing: %s", out)
	}
	if urls["8080"] != "http://8080-abc-123.1.2.3.4.nip.io:4000" {
		t.Errorf("preview_urls[8080] = %v", urls["8080"])
	}
	if urls["3000"] != "http://3000-abc-123.1.2.3.4.nip.io:4000" {
		t.Errorf("preview_urls[3000] = %v", urls["3000"])
	}
}

func TestDecoratePublicPorts_UnsupportedBackend(t *testing.T) {
	// Stock Daytona strips the field: response carries no publicPorts.
	resp := `{"id":"abc-123","state":"started"}`
	out, supported, err := decoratePublicPorts([]byte(resp))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if supported {
		t.Fatal("response without publicPorts must report unsupported")
	}
	if string(out) != resp {
		t.Error("body must be unchanged when unsupported")
	}
}

func TestDecoratePublicPorts_NoProxyDomain(t *testing.T) {
	os.Unsetenv("PROXY_DOMAIN")
	resp := `{"id":"abc-123","publicPorts":[8080]}`
	out, supported, err := decoratePublicPorts([]byte(resp))
	if err != nil || !supported {
		t.Fatalf("supported=%v err=%v", supported, err)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if _, has := m["preview_urls"]; has {
		t.Error("preview_urls must be skipped without PROXY_DOMAIN")
	}
}
