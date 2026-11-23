package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/daytona"
	"github.com/0gfoundation/0g-sandbox/internal/usage"
)

type fakeUsage struct {
	gotID   string
	gotDisk int
}

func (f *fakeUsage) Report(_ context.Context, id string, disk int) usage.Report {
	f.gotID, f.gotDisk = id, disk
	return usage.Report{SandboxID: id, Measured: true, Disk: usage.DiskReport{RequestedBytes: int64(disk) * usage.GiB, Status: "ok"}}
}

func usageEngine(t *testing.T, sealed bool, wallet string, u UsageReporter) *gin.Engine {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		labels := map[string]string{ownerLabel: "0xOWNER"}
		if sealed {
			labels[sealedLabel] = "true"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "sb-u", "labels": labels, "disk": 10})
	}))
	t.Cleanup(srv.Close)
	r := gin.New()
	api := r.Group("/api", func(c *gin.Context) { c.Set("wallet_address", wallet); c.Next() })
	h := NewHandler(daytona.NewClient(srv.URL, "k"), &mockBilling{}, nil, nil, nil, nil, nil, nil, nil, "", nil, "", nil, zap.NewNop(), "", nil, nil, 0)
	if u != nil {
		h.Usage = u
	}
	h.Register(api)
	return r
}

func TestUsage_Route(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sealed bool
		wallet string
		on     bool
		path   string
		want   int
	}{
		{"owner", false, "0xOWNER", true, "/api/sandbox/sb-u/usage", http.StatusOK},
		{"sealed owner", true, "0xowner", true, "/api/sandbox/sb-u/usage", http.StatusOK},
		{"not owner", false, "0xOTHER", true, "/api/sandbox/sb-u/usage", http.StatusForbidden},
		{"monitoring off", false, "0xOWNER", false, "/api/sandbox/sb-u/usage", http.StatusServiceUnavailable},
		{"case alias", false, "0xOWNER", true, "/api/sandbox/sb-u/Usage", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeUsage{}
			var u UsageReporter
			if tc.on {
				u = f
			}
			w := httptest.NewRecorder()
			usageEngine(t, tc.sealed, tc.wallet, u).ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != tc.want {
				t.Fatalf("got %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusOK {
				var rep usage.Report
				if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil || rep.SandboxID != "sb-u" || rep.Disk.RequestedBytes != 10*usage.GiB {
					t.Errorf("report = %+v, %v", rep, err)
				}
				if f.gotID != "sb-u" || f.gotDisk != 10 {
					t.Errorf("reporter got (%q, %d), want the sandbox's ID and requested disk", f.gotID, f.gotDisk)
				}
			}
		})
	}
}
