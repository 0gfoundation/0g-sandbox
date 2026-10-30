package proxy

import "testing"

func TestRequireSnapshotCreate(t *testing.T) {
	ok := []string{
		`{"snapshot":"ubuntu-2c"}`,
		`{"snapshot":"ubuntu-2c","name":"x","sealed":true}`,
		`{"snapshot":"ubuntu-2c","publicPorts":[8080]}`,
	}
	for _, b := range ok {
		if err := requireSnapshotCreate([]byte(b)); err != nil {
			t.Errorf("must accept %s: %v", b, err)
		}
	}

	bad := []string{
		`{"snapshot":"x","cpu":2}`,          // custom cpu
		`{"snapshot":"x","CPU":2}`,          // case variant — the #73 vector
		`{"snapshot":"x","Cpu":2}`,          // another case variant
		`{"snapshot":"x","memory":4}`,       // custom memory
		`{"snapshot":"x","disk":20}`,        // custom disk
		`{"snapshot":"x","gpu":1}`,          // custom gpu
		`{"image":"ubuntu:22.04"}`,          // bare image — the #77 vector
		`{"snapshot":"x","image":"ubuntu"}`, // image alongside snapshot
		`{"snapshot":"x","class":"large"}`,  // class size-tier selector
		`{"snapshot":"x","Class":"large"}`,  // class case variant
		`{"snapshot":"x","buildInfo":{}}`,   // declarative build path
		`{"name":"nosnap"}`,                 // no snapshot at all
		`{}`,                                // empty
		`{"snapshot":""}`,                   // empty snapshot
		`{"snapshot":123}`,                  // wrong type
	}
	for _, b := range bad {
		if err := requireSnapshotCreate([]byte(b)); err == nil {
			t.Errorf("must reject %s", b)
		}
	}
}

func TestRejectSnapshotOverrides(t *testing.T) {
	for _, k := range []string{"entrypoint", "Entrypoint", "ENTRYPOINT", "buildInfo", "buildinfo", "BuildInfo"} {
		if err := rejectSnapshotOverrides(map[string]any{"name": "s", "imageName": "img", k: nil}); err == nil {
			t.Errorf("%q accepted", k)
		}
	}
	allowed := map[string]any{"name": "s", "imageName": "img", "cpu": 1, "gpu": 0, "memory": 1, "disk": 3, "general": false, "regionId": "r"}
	if err := rejectSnapshotOverrides(allowed); err != nil {
		t.Fatalf("plain snapshot rejected: %v", err)
	}
}
