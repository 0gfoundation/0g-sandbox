package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/daytona"
	"github.com/0gfoundation/0g-sandbox/internal/notice"
)

// NoticePusher sends every sandbox's notices to the runner.
type NoticePusher interface {
	PushNotices(ctx context.Context, set notice.Set, validFor time.Duration) error
}

// EnableNotices makes every poll send the running sandboxes their notices:
// the Monitor's own about disk, and those of the sources. Call before Run.
func (m *Monitor) EnableNotices(p NoticePusher, sources ...notice.Source) {
	m.pusher, m.sources = p, sources
}

// noticeValidity is how long the runner shows a set unless replaced: a few
// polls, so one missed poll changes nothing, and stale warnings still lapse.
func (m *Monitor) noticeValidity() time.Duration {
	return min(max(3*m.cfg.Interval, 5*time.Minute), time.Hour)
}

func (m *Monitor) sendNotices(ctx context.Context, now time.Time, snap *Snapshot, sandboxes []daytona.Sandbox) {
	if m.pusher == nil {
		return
	}
	set := notice.Set{}
	var running []string
	for _, sb := range sandboxes {
		if !strings.EqualFold(sb.State, "started") {
			continue
		}
		running = append(running, sb.ID)
		if n := m.diskNotice(ctx, now, snap, sb); n != nil {
			set.Add(sb.ID, *n)
		}
	}
	for _, src := range m.sources {
		s, err := src.Notices(ctx, running)
		if err != nil {
			m.log.Warn("usage: gather notices", zap.String("source", fmt.Sprintf("%T", src)), zap.Error(err))
			continue
		}
		set.Merge(s)
	}
	m.mu.Lock()
	m.lastNotices = set
	m.mu.Unlock()

	err := m.pusher.PushNotices(ctx, set, m.noticeValidity())
	switch {
	case err != nil && !m.noticesFailing:
		m.noticesFailing = true
		m.log.Warn("usage: send notices to the runner; sandboxes are not told what is coming", zap.Error(err))
	case err == nil && m.noticesFailing:
		m.noticesFailing = false
		m.log.Info("usage: notices reach the runner again")
	}
}

// diskNotice warns a running sandbox near or past its disk size, from a size
// measured recently enough to act on.
func (m *Monitor) diskNotice(ctx context.Context, now time.Time, snap *Snapshot, sb daytona.Sandbox) *notice.Notice {
	u := snap.Sandboxes[sb.ID]
	if u == nil || sb.Disk <= 0 || u.Used() == nil || now.Sub(u.LayerMeasuredAt) > maxLayerAge {
		return nil
	}
	used, size := *u.Used(), int64(sb.Disk)*GiB
	const deleteHint = " Delete files you no longer need: caches, temporary files, old downloads and logs. Disk use is measured again every few minutes."
	switch {
	case used > size && !m.cfg.Enforce:
		return &notice.Notice{Kind: notice.KindDiskOverSize, Severity: notice.SeverityWarning,
			Message: fmt.Sprintf("This sandbox holds %s of data in its own filesystem, past its %s disk. This provider does not stop sandboxes for that yet, but may start to.", bytesText(used), bytesText(size)) + deleteHint}
	case used > size:
		rec, err := GetDiskStop(ctx, m.rdb, sb.ID)
		if err != nil {
			m.log.Warn("usage: read disk-stop record", zap.String("sandbox", sb.ID), zap.Error(err))
		}
		critical := m.cfg.VolumeCriticalPct > 0 && snap.Volume.UsedPct() >= m.cfg.VolumeCriticalPct
		if rec != nil && rec.Phase == PhaseGrace && rec.GraceUntil != nil && now.Before(*rec.GraceUntil) && !critical {
			until := *rec.GraceUntil
			return &notice.Notice{Kind: notice.KindDiskOverSize, Severity: notice.SeverityCritical, Deadline: &until, Action: notice.ActionStop,
				Message: fmt.Sprintf("This sandbox holds %s of data in its own filesystem, past its %s disk. It was stopped for this once. Unless it is back under %s by %s, it is stopped again.", bytesText(used), bytesText(size), bytesText(size), until.UTC().Format("2006-01-02 15:04 UTC")) + deleteHint}
		}
		text := fmt.Sprintf("This sandbox holds %s of data in its own filesystem, past its %s disk, and is being stopped for it. Save your work now. Files are kept while it is stopped.", bytesText(used), bytesText(size))
		if m.cfg.Grace > 0 {
			text += fmt.Sprintf(" When the owner starts it again, it has %s to get back under %s.", m.cfg.Grace.Round(time.Minute), bytesText(size))
		}
		at := now.UTC().Truncate(time.Second)
		if rec != nil { // the stop's own time, the same on every poll
			at = rec.QueuedAt.UTC().Truncate(time.Second)
		}
		return &notice.Notice{Kind: notice.KindDiskOverSize, Severity: notice.SeverityCritical, Deadline: &at, Action: notice.ActionStop, Message: text}
	case size-used < m.cfg.WarnFreeBytes:
		n := &notice.Notice{Kind: notice.KindDiskNearFull, Severity: notice.SeverityWarning,
			Message: fmt.Sprintf("This sandbox holds %s of data in its own filesystem; its disk is %s, so %s is left.", bytesText(used), bytesText(size), bytesText(size-used))}
		if m.cfg.Enforce {
			n.Action = notice.ActionStop
			n.Message += fmt.Sprintf(" Past %s this provider stops the sandbox.", bytesText(size))
		}
		n.Message += deleteHint
		return n
	}
	return nil
}

// bytesText writes a size for a message.
func bytesText(b int64) string {
	if b >= GiB {
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(GiB))
	}
	return fmt.Sprintf("%d MiB", b>>20)
}

// PushNotices implements NoticePusher: PUT /sandbox-notices on the runner
// (0g-daytona#19).
func (c *RunnerClient) PushNotices(ctx context.Context, set notice.Set, validFor time.Duration) error {
	if set == nil {
		set = notice.Set{}
	}
	body, err := json.Marshal(struct {
		ValidForSec int64      `json:"valid_for_sec"`
		Sandboxes   notice.Set `json:"sandboxes"`
	}{int64(validFor / time.Second), set})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.url+"/sandbox-notices", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("runner notices: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("runner notices: status %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}
	return nil
}
