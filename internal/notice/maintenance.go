package notice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// Maintenance is the provider's planned maintenance, set by an admin. Every
// running sandbox is told from then until it is over. A provider restart
// stops and archives the running sandboxes (POST /api/archive-all): files
// are kept, running processes are not.
type Maintenance struct {
	At time.Time `json:"at"`
	// Message is the operator's own words, added to the standard text.
	Message string    `json:"message,omitempty"`
	SetAt   time.Time `json:"set_at"`
	SetBy   string    `json:"set_by,omitempty"`
}

const (
	maintenanceKey = "notice:maintenance"
	// A plan is shown until this long after its time, then expires.
	maintenanceShownAfter = time.Hour
	maxMaintenanceAhead   = 30 * 24 * time.Hour
	maxMaintenanceMessage = 500
)

// GetMaintenance returns the plan, or nil if there is none.
func GetMaintenance(ctx context.Context, rdb *redis.Client) (*Maintenance, error) {
	raw, err := rdb.Get(ctx, maintenanceKey).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Maintenance
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func putMaintenance(ctx context.Context, rdb *redis.Client, m Maintenance, now time.Time) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, maintenanceKey, raw, m.At.Add(maintenanceShownAfter).Sub(now)).Err()
}

// MaintenanceSource tells every running sandbox of the plan.
type MaintenanceSource struct {
	RDB *redis.Client
	Now func() time.Time
}

// Notices implements Source.
func (s MaintenanceSource) Notices(ctx context.Context, running []string) (Set, error) {
	m, err := GetMaintenance(ctx, s.RDB)
	if err != nil || m == nil || len(running) == 0 {
		return nil, err
	}
	now := s.Now()
	if now.After(m.At.Add(maintenanceShownAfter)) {
		return nil, nil
	}
	at := m.At
	n := Notice{Kind: KindMaintenance, Deadline: &at, Action: ActionArchive}
	switch {
	case m.At.Sub(now) > 24*time.Hour:
		n.Severity = SeverityInfo
	default:
		n.Severity = severityBy(m.At, now)
	}
	if now.Before(m.At) {
		n.Message = fmt.Sprintf("This provider plans maintenance at %s. Running sandboxes are then stopped and archived: files are kept, running processes and anything held only in memory are not. The owner starts the sandbox again afterwards. Save your work before then.", utc(m.At))
	} else {
		n.Message = fmt.Sprintf("This provider's maintenance, planned for %s, is under way: this sandbox may be stopped and archived at any moment. Files are kept, running processes are not. Save your work now.", utc(m.At))
	}
	if m.Message != "" {
		n.Message += " The provider says: " + m.Message
	}
	set := Set{}
	for _, id := range running {
		set.Add(id, n)
	}
	return set, nil
}

// HandleGetMaintenance serves GET /api/maintenance: the plan, or
// {"maintenance": null}.
func HandleGetMaintenance(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		m, err := GetMaintenance(c.Request.Context(), rdb)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read the maintenance plan"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"maintenance": m})
	}
}

// HandleSetMaintenance serves PUT /api/maintenance (admins): {"at": RFC 3339,
// "message": optional}. It replaces any earlier plan.
func HandleSetMaintenance(rdb *redis.Client, isAdmin func(wallet string) bool, now func() time.Time) gin.HandlerFunc {
	return func(c *gin.Context) {
		wallet := c.GetString("wallet_address")
		if !isAdmin(wallet) {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
			return
		}
		var req struct {
			At      time.Time `json:"at"`
			Message string    `json:"message"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "body must be {\"at\": RFC 3339 time, \"message\": optional text}"})
			return
		}
		t := now()
		req.Message = strings.TrimSpace(req.Message)
		switch {
		case !req.At.After(t) || req.At.Sub(t) > maxMaintenanceAhead:
			c.JSON(http.StatusBadRequest, gin.H{"error": "at must be in the next 30 days"})
			return
		case len(req.Message) > maxMaintenanceMessage || !utf8.ValidString(req.Message):
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("message must be at most %d bytes of UTF-8", maxMaintenanceMessage)})
			return
		}
		m := Maintenance{At: req.At.UTC(), Message: req.Message, SetAt: t.UTC(), SetBy: wallet}
		if err := putMaintenance(c.Request.Context(), rdb, m, t); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not store the maintenance plan"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"maintenance": m})
	}
}

// HandleClearMaintenance serves DELETE /api/maintenance (admins).
func HandleClearMaintenance(rdb *redis.Client, isAdmin func(wallet string) bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isAdmin(c.GetString("wallet_address")) {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
			return
		}
		if err := rdb.Del(c.Request.Context(), maintenanceKey).Err(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not clear the maintenance plan"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}
