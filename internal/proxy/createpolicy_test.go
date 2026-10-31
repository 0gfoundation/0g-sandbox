package proxy

import (
	"strings"
	"testing"
)

func TestRequireSnapshotCreate(t *testing.T) {
	ok := []string{
		`{"snapshot":"ubuntu-2c"}`,
		`{"snapshot":"ubuntu-2c","name":"x","sealed":true}`,
		`{"snapshot":"ubuntu-2c","publicPorts":[8080]}`,
		`{"snapshot":"ubuntu-2c","Sealed":true}`, // one spelling: every reader folds sealed
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
		// Field names that differ only by case: Go's struct decode folds them
		// (last wins), Daytona reads the exact key.
		`{"snapshot":"x","Snapshot":"y"}`,
		`{"Snapshot":"y","snapshot":"x"}`,
		`{"snapshot":"x","ſnapshot":"y"}`, // U+017F folds to s
		`{"ſnapshot":"y"}`,
		`{"SNAPSHOT":"y"}`,
		`{"snapshot":"x","sealed":false,"Sealed":true}`,
		`{"snapshot":"x","publicPorts":[8080],"publicPortſ":[22]}`,
		`{"snapshot":"x","diſk":20}`,   // folded banned field
		`{"snapshot":"x","claſſ":"l"}`, // folded banned field
	}
	for _, b := range bad {
		if err := requireSnapshotCreate([]byte(b)); err == nil {
			t.Errorf("must reject %s", b)
		}
	}
}

func TestFoldNameMatchesEqualFold(t *testing.T) {
	words := []string{"snapshot", "Snapshot", "ſnapshot", "SNAPſHOT", "sealed", "ſealed", "kelvin", "\u212Aelvin", "Kelvin", "image", "ımage", "İmage", "env", "ENV", "labels"}
	for _, a := range words {
		for _, b := range words {
			if got, want := foldName(a) == foldName(b), strings.EqualFold(a, b); got != want {
				t.Errorf("foldName(%q)==foldName(%q) is %v; EqualFold %v", a, b, got, want)
			}
		}
	}
}
