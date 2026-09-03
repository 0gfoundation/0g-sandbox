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
