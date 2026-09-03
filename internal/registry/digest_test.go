package registry

import "testing"

// PinRef must produce a content-addressed reference from every caller-supplied
// shape: tagged, port-carrying registry hosts, bare names, and refs that are
// already digest-pinned (idempotent).
func TestPinRef(t *testing.T) {
	const d = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cases := []struct{ in, want string }{
		{"registry:6000/daytona/app:latest", "registry:6000/daytona/app@" + d},
		{"registry:6000/daytona/app", "registry:6000/daytona/app@" + d},
		{"ubuntu:22.04", "index.docker.io/library/ubuntu@" + d},
		{"registry:6000/daytona/app@" + d, "registry:6000/daytona/app@" + d}, // already pinned → same
	}
	for _, c := range cases {
		got, err := PinRef(c.in, d)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("PinRef(%s):\n got  %s\n want %s", c.in, got, c.want)
		}
	}
	if _, err := PinRef(":::not a ref", d); err == nil {
		t.Error("invalid ref must error")
	}
}
