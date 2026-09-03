package proxy

import (
	"encoding/json"
	"testing"
)

// ── InjectOwner ───────────────────────────────────────────────────────────────

func TestInjectOwner_EmptyBody(t *testing.T) {
	wallet := "0xABCD"
	out, err := InjectOwner(nil, wallet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck

	labels, ok := m["labels"].(map[string]any)
	if !ok {
		t.Fatal("labels field missing or wrong type")
	}
	if labels[ownerLabel] != wallet {
		t.Errorf("daytona-owner: got %v want %v", labels[ownerLabel], wallet)
	}
	if m["autoStopInterval"] != float64(0) {
		t.Errorf("autoStopInterval: got %v want 0", m["autoStopInterval"])
	}
	if m["autoArchiveInterval"] != float64(60) {
		t.Errorf("autoArchiveInterval: got %v want 60", m["autoArchiveInterval"])
	}
	// Private by default: a plain create with no publicPorts must not be public.
	if m["public"] != false {
		t.Errorf("public: got %v want false", m["public"])
	}
}

func TestInjectOwner_PrivateByDefault_PublicOnOptIn(t *testing.T) {
	// Port exposure is private by default; the sandbox is only made public when
	// the caller opts specific ports in via publicPorts (or it is sealed, which
	// exposes just the agent port :8080). A bare {"public":true} must NOT reopen
	// the all-ports-public hole.
	cases := []struct {
		name          string
		body          []byte
		wantPublic    bool
		wantPortsOnly []int // if non-nil, publicPorts must equal exactly this
	}{
		{"empty body", nil, false, nil},
		{"with image, no ports", []byte(`{"image":"ubuntu:22.04"}`), false, nil},
		{"caller sets public:true, no ports", []byte(`{"public":true}`), false, nil},
		{"publicPorts given", []byte(`{"publicPorts":[8080,3000]}`), true, nil},
		{"sealed, no ports", []byte(`{"image":"my-img","sealed":true}`), true, []int{8080}},
		{"mixed-case Sealed, no ports", []byte(`{"image":"my-img","Sealed":true}`), true, []int{8080}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := InjectOwner(tc.body, "0xW")
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			json.Unmarshal(out, &m) //nolint:errcheck
			if m["public"] != tc.wantPublic {
				t.Errorf("public: got %v want %v", m["public"], tc.wantPublic)
			}
			if tc.wantPortsOnly != nil {
				pp, ok := m["publicPorts"].([]any)
				if !ok || len(pp) != len(tc.wantPortsOnly) {
					t.Fatalf("publicPorts: got %v want %v", m["publicPorts"], tc.wantPortsOnly)
				}
				for i, want := range tc.wantPortsOnly {
					if int(pp[i].(float64)) != want {
						t.Errorf("publicPorts[%d]: got %v want %d", i, pp[i], want)
					}
				}
			}
		})
	}
}

func TestInjectOwner_OverwritesExistingOwner(t *testing.T) {
	wallet := "0xLEGIT"
	body := []byte(`{"labels":{"daytona-owner":"0xATTACKER","other":"val"}}`)

	out, err := InjectOwner(body, wallet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck
	labels := m["labels"].(map[string]any)

	if labels[ownerLabel] != wallet {
		t.Errorf("daytona-owner should be overwritten: got %v", labels[ownerLabel])
	}
	if labels["other"] != "val" {
		t.Error("other labels should be preserved")
	}
}

func TestInjectOwner_PreservesOtherFields(t *testing.T) {
	body := []byte(`{"name":"my-sandbox","image":"ubuntu"}`)
	out, err := InjectOwner(body, "0xWALLET")
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck

	if m["name"] != "my-sandbox" {
		t.Errorf("name field lost: %v", m["name"])
	}
	if m["image"] != "ubuntu" {
		t.Errorf("image field lost: %v", m["image"])
	}
}

func TestInjectOwner_ForcesAutostopToZero(t *testing.T) {
	// User tries to set autostop via either casing; proxy must override with correct values.
	body := []byte(`{"autostopInterval":3600,"autoarchiveInterval":7200,"autoStopInterval":9999}`)
	out, err := InjectOwner(body, "0xW")
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck

	// Proxy always sets autoStopInterval=0 (Daytona's canonical field name).
	if m["autoStopInterval"] != float64(0) {
		t.Errorf("autoStopInterval should be 0, got %v", m["autoStopInterval"])
	}
	// Proxy always sets autoArchiveInterval=60 as a crash-safety fallback.
	if m["autoArchiveInterval"] != float64(60) {
		t.Errorf("autoArchiveInterval should be 60, got %v", m["autoArchiveInterval"])
	}
}

func TestInjectOwner_InvalidJSON(t *testing.T) {
	_, err := InjectOwner([]byte(`not json`), "0xW")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ── StripOwnerLabel ───────────────────────────────────────────────────────────

func TestStripOwnerLabel_RemovesKey(t *testing.T) {
	body := []byte(`{"daytona-owner":"0xHACKER","env":"prod"}`)
	out, err := StripOwnerLabel(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck

	if _, exists := m[ownerLabel]; exists {
		t.Error("daytona-owner should have been stripped")
	}
	if m["env"] != "prod" {
		t.Error("other keys should be preserved")
	}
}

func TestStripOwnerLabel_KeyAbsent(t *testing.T) {
	body := []byte(`{"foo":"bar"}`)
	out, err := StripOwnerLabel(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck
	if m["foo"] != "bar" {
		t.Error("existing keys should be preserved when daytona-owner is absent")
	}
}

func TestStripOwnerLabel_InvalidJSON(t *testing.T) {
	_, err := StripOwnerLabel([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestStripOwnerLabel_EmptyObject(t *testing.T) {
	out, err := StripOwnerLabel([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "{}" {
		t.Errorf("unexpected output: %s", out)
	}
}

// ── Sealed container ──────────────────────────────────────────────────────────

func TestInjectOwner_SealedTrue_InjectsLabel(t *testing.T) {
	body := []byte(`{"image":"ubuntu:22.04","sealed":true}`)
	out, err := InjectOwner(body, "0xW")
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck

	labels := m["labels"].(map[string]any)
	if labels[sealedLabel] != "true" {
		t.Errorf("0g-sealed label not set: labels=%v", labels)
	}
	// sealed field must be stripped from body before forwarding to Daytona
	if _, exists := m["sealed"]; exists {
		t.Error("sealed field must be removed from forwarded body")
	}
}

// Regression for the case-mismatch bug: extractSealed (which drives seal-key
// injection in handleCreate) matches "sealed" case-insensitively, so a mixed-case
// {"Sealed":true} injects the key. InjectOwner must set 0g-sealed for the SAME
// input — otherwise SSH/toolbox stay open on a sealed workload — and strip the
// field regardless of case so it never reaches Daytona.
func TestInjectOwner_SealedMixedCase_InjectsLabelAndStrips(t *testing.T) {
	for _, key := range []string{"Sealed", "SEALED", "sEaLeD"} {
		body := []byte(`{"image":"ubuntu:22.04","` + key + `":true}`)
		out, err := InjectOwner(body, "0xW")
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}

		var m map[string]any
		json.Unmarshal(out, &m) //nolint:errcheck

		labels := m["labels"].(map[string]any)
		if labels[sealedLabel] != "true" {
			t.Errorf("%s: 0g-sealed label not set — SSH/toolbox would stay open: labels=%v", key, labels)
		}
		if _, exists := m[key]; exists {
			t.Errorf("%s: sealed field (any case) must be stripped from forwarded body", key)
		}
	}
}

func TestInjectOwner_SealedFalse_NoLabel(t *testing.T) {
	body := []byte(`{"image":"ubuntu:22.04","sealed":false}`)
	out, err := InjectOwner(body, "0xW")
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck

	labels := m["labels"].(map[string]any)
	if labels[sealedLabel] == "true" {
		t.Error("0g-sealed should not be set when sealed=false")
	}
	if _, exists := m["sealed"]; exists {
		t.Error("sealed field must be removed from forwarded body")
	}
}

// Deny-by-default: a caller-supplied volumes array must never be forwarded to
// Daytona — the proxy speaks as admin and does not validate volume ownership, so
// forwarding it would let a caller mount another tenant's volume. Any case.
func TestInjectOwner_StripsVolumes(t *testing.T) {
	for _, key := range []string{"volumes", "Volumes", "VOLUMES"} {
		body := []byte(`{"image":"ubuntu","` + key + `":[{"volumeId":"victim-vol","mountPath":"/mnt/v"}]}`)
		out, err := InjectOwner(body, "0xW")
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		var m map[string]any
		json.Unmarshal(out, &m) //nolint:errcheck
		if _, exists := m[key]; exists {
			t.Errorf("%s: volumes must be stripped from forwarded body", key)
		}
	}
}

func TestInjectOwner_RecordsImageLabel(t *testing.T) {
	body := []byte(`{"image":"ubuntu:22.04"}`)
	out, err := InjectOwner(body, "0xW")
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck

	labels := m["labels"].(map[string]any)
	if labels[imageLabel] != "ubuntu:22.04" {
		t.Errorf("0g-image label: got %v want ubuntu:22.04", labels[imageLabel])
	}
}

func TestInjectOwner_RecordsSnapshotLabel(t *testing.T) {
	body := []byte(`{"snapshot":"snap-abc"}`)
	out, err := InjectOwner(body, "0xW")
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck

	labels := m["labels"].(map[string]any)
	if labels[imageLabel] != "snapshot:snap-abc" {
		t.Errorf("0g-image label: got %v want snapshot:snap-abc", labels[imageLabel])
	}
}

func TestStripOwnerLabel_AlsoStripsSealed(t *testing.T) {
	body := []byte(`{"daytona-owner":"0xHACKER","0g-sealed":"true","env":"prod"}`)
	out, err := StripOwnerLabel(body)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck

	if _, exists := m[sealedLabel]; exists {
		t.Error("0g-sealed should have been stripped (immutable once set)")
	}
	if m["env"] != "prod" {
		t.Error("other keys should be preserved")
	}
}

// Review F1: caller-supplied case variants of public/publicPorts must not
// survive into the forwarded body — a case-insensitive consumer downstream
// could otherwise resurrect the all-ports-public hole via {"PublicPorts": ...}.
func TestInjectOwner_MixedCasePortFieldsStripped(t *testing.T) {
	out, err := InjectOwner([]byte(`{"PublicPorts":[3284],"Public":true}`), "0xW")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck
	if _, exists := m["PublicPorts"]; exists {
		t.Error("mixed-case PublicPorts must be stripped from the forwarded body")
	}
	if _, exists := m["Public"]; exists {
		t.Error("mixed-case Public must be stripped from the forwarded body")
	}
	if m["public"] != false {
		t.Errorf("mixed-case fields must not grant public; got %v", m["public"])
	}
}
