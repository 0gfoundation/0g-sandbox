package notice

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const admin = "0xAdmin"

func maintenanceRouter(t *testing.T, now *time.Time) (*gin.Engine, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("wallet_address", c.GetHeader("X-Wallet")) })
	isAdmin := func(w string) bool { return w == admin }
	r.GET("/maintenance", HandleGetMaintenance(rdb))
	r.PUT("/maintenance", HandleSetMaintenance(rdb, isAdmin, func() time.Time { return *now }))
	r.DELETE("/maintenance", HandleClearMaintenance(rdb, isAdmin))
	return r, rdb, mr
}

func call(r *gin.Engine, method, wallet, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/maintenance", strings.NewReader(body))
	req.Header.Set("X-Wallet", wallet)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestMaintenance_AdminSetsEveryRunningSandboxIsTold(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	r, rdb, mr := maintenanceRouter(t, &now)
	src := MaintenanceSource{RDB: rdb, Now: func() time.Time { return now }}

	if w := call(r, http.MethodPut, "0xSomeone", `{"at":"2026-11-25T02:00:00Z"}`); w.Code != http.StatusForbidden {
		t.Errorf("non-admin: %d", w.Code)
	}
	for _, body := range []string{`{"at":"2026-11-24T09:00:00Z"}`, `{"at":"2027-01-30T00:00:00Z"}`, `{"at":"tomorrow"}`,
		`{"at":"2026-11-25T02:00:00Z","message":"` + strings.Repeat("x", 501) + `"}`} {
		if w := call(r, http.MethodPut, admin, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body[:30], w.Code)
		}
	}
	if set, _ := src.Notices(context.Background(), []string{"sb-1"}); len(set) != 0 {
		t.Errorf("no plan: %+v", set)
	}

	if w := call(r, http.MethodPut, admin, `{"at":"2026-11-25T02:00:00+08:00","message":" Kernel upgrade. "}`); w.Code != http.StatusOK {
		t.Fatalf("set: %d %s", w.Code, w.Body)
	}
	if w := call(r, http.MethodGet, "0xSomeone", ""); !strings.Contains(w.Body.String(), `"at":"2026-11-24T18:00:00Z"`) {
		t.Errorf("get: %s", w.Body)
	}
	set, err := src.Notices(context.Background(), []string{"sb-1", "sb-2"})
	if err != nil || len(set) != 2 {
		t.Fatalf("plan: %+v, %v; want both running sandboxes told", set, err)
	}
	n := set["sb-2"][0]
	if n.Kind != KindMaintenance || n.Severity != SeverityWarning || n.Action != ActionArchive || !n.Deadline.Equal(time.Date(2026, 11, 24, 18, 0, 0, 0, time.UTC)) ||
		!strings.Contains(n.Message, "2026-11-24 18:00 UTC") || !strings.HasSuffix(n.Message, "The provider says: Kernel upgrade.") {
		t.Errorf("notice: %+v", n)
	}

	now = time.Date(2026, 11, 24, 17, 50, 0, 0, time.UTC)
	if set, _ := src.Notices(context.Background(), []string{"sb-1"}); set["sb-1"][0].Severity != SeverityCritical {
		t.Errorf("10 min before: %+v, want critical", set["sb-1"])
	}
	now = time.Date(2026, 11, 24, 18, 30, 0, 0, time.UTC)
	if set, _ := src.Notices(context.Background(), []string{"sb-1"}); !strings.Contains(set["sb-1"][0].Message, "is under way") {
		t.Errorf("during: %+v", set["sb-1"])
	}
	// The record expires an hour after the plan.
	if ttl := mr.TTL(maintenanceKey); ttl != 9*time.Hour {
		t.Errorf("TTL = %s, want until an hour after the plan (9h from when it was set)", ttl)
	}

	if w := call(r, http.MethodDelete, "0xSomeone", ""); w.Code != http.StatusForbidden {
		t.Errorf("non-admin clear: %d", w.Code)
	}
	if w := call(r, http.MethodDelete, admin, ""); w.Code != http.StatusNoContent {
		t.Errorf("clear: %d", w.Code)
	}
	if set, _ := src.Notices(context.Background(), []string{"sb-1"}); len(set) != 0 {
		t.Errorf("after clear: %+v", set)
	}
	if w := call(r, http.MethodGet, "0xSomeone", ""); !strings.Contains(w.Body.String(), `"maintenance":null`) {
		t.Errorf("get after clear: %s", w.Body)
	}
}
