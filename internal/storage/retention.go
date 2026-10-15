package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Policy bounds how much history is kept. Zero fields take the defaults.
type Policy struct {
	Days  int // sessions that ended more than this many days ago are removed
	MaxMB int // total recorded size limit; oldest closed sessions go first
}

// Defaults match the config defaults.
const (
	DefaultRetentionDays = 7
	DefaultMaxTotalMB    = 500
)

func (p *Policy) defaults() {
	if p.Days <= 0 {
		p.Days = DefaultRetentionDays
	}
	if p.MaxMB <= 0 {
		p.MaxMB = DefaultMaxTotalMB
	}
}

// Enforce applies the retention policy. Active sessions and protectID are
// never removed. Oldest closed sessions go first: first everything past
// Days, then, while the total exceeds MaxMB, the oldest remaining. With
// dryRun nothing is deleted; the result says what would be.
func (s *Store) Enforce(ctx context.Context, p Policy, protectID string, dryRun bool) (api.RetentionResult, error) {
	p.defaults()
	res := api.RetentionResult{SchemaVersion: api.SchemaVersion, DryRun: dryRun,
		RetentionDays: p.Days, MaxTotalMB: p.MaxMB, Removed: []api.RemovedSession{}}

	rows, err := s.db.QueryContext(ctx, `SELECT id, status, ended_ns, started_ns, bytes FROM sessions ORDER BY COALESCE(ended_ns, started_ns), id`)
	if err != nil {
		return res, err
	}
	type sess struct {
		id     string
		status string
		ended  int64
		bytes  int64
	}
	var all []sess
	for rows.Next() {
		var (
			x       sess
			ended   sql.NullInt64
			started int64
		)
		if err := rows.Scan(&x.id, &x.status, &ended, &started, &x.bytes); err != nil {
			rows.Close()
			return res, err
		}
		x.ended = started
		if ended.Valid {
			x.ended = ended.Int64
		}
		all = append(all, x)
		res.TotalBefore += x.bytes
	}
	if err := rows.Close(); err != nil {
		return res, err
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ended < all[j].ended })

	total := res.TotalBefore
	cutoff := s.opts.Now().Add(-time.Duration(p.Days) * 24 * time.Hour).UnixNano()
	limit := int64(p.MaxMB) << 20
	removed := map[string]bool{}
	remove := func(x sess, reason string) error {
		res.Removed = append(res.Removed, api.RemovedSession{ID: x.id, Reason: reason, SizeBytes: x.bytes})
		removed[x.id] = true
		total -= x.bytes
		if dryRun {
			return nil
		}
		return s.removeSession(ctx, x.id)
	}
	protected := func(x sess) bool { return x.status == api.SessionActive || x.id == protectID }

	var firstErr error
	for _, x := range all {
		if protected(x) || x.ended >= cutoff {
			continue
		}
		if err := remove(x, "age"); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, x := range all {
		if total <= limit {
			break
		}
		if protected(x) || removed[x.id] {
			continue
		}
		if err := remove(x, "size"); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if total > limit {
		for _, x := range all {
			if protected(x) && !removed[x.id] {
				res.Skipped = append(res.Skipped, x.id)
			}
		}
	}
	res.TotalAfter = total
	res.Kept = len(all) - len(removed)

	if !dryRun && len(removed) > 0 {
		// Return freed pages to the OS and shrink the WAL; best effort.
		_, _ = s.db.ExecContext(ctx, `PRAGMA incremental_vacuum`)
		_, _ = s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`)
		s.sweepOrphans(ctx)
	}
	return res, firstErr
}

// sweepOrphans removes session directories that no session row refers to
// (left by a crash between deleting rows and files).
func (s *Store) sweepOrphans(ctx context.Context) {
	entries, err := os.ReadDir(sessionsDir(s.dir))
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id = ?`, e.Name()).Scan(&n); err == nil && n == 0 {
			_ = os.RemoveAll(filepath.Join(sessionsDir(s.dir), e.Name()))
		}
	}
}

// RecoverStale marks active sessions whose Kestrel process is gone as
// interrupted and seals their open segments. alive reports whether a PID is
// running. It returns how many sessions it recovered.
func (s *Store) RecoverStale(ctx context.Context, alive func(pid int) bool) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kestrel_pid, started_ns FROM sessions WHERE status = 'active'`)
	if err != nil {
		return 0, err
	}
	type st struct {
		id      string
		pid     int
		started int64
	}
	var stale []st
	for rows.Next() {
		var x st
		if err := rows.Scan(&x.id, &x.pid, &x.started); err != nil {
			rows.Close()
			return 0, err
		}
		if !alive(x.pid) {
			stale = append(stale, x)
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, x := range stale {
		var last sql.NullInt64
		_ = s.db.QueryRowContext(ctx, `SELECT MAX(time_ns) FROM events WHERE session_id = ?`, x.id).Scan(&last)
		ended := x.started
		if last.Valid && last.Int64 > ended {
			ended = last.Int64
		}
		if err := s.sealOpenSegments(ctx, x.id); err != nil {
			return 0, err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET status = 'interrupted', ended_ns = ? WHERE id = ?`, ended, x.id); err != nil {
			return 0, err
		}
	}
	return len(stale), nil
}

func (s *Store) sealOpenSegments(ctx context.Context, sessionID string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id, path FROM segments WHERE session_id = ? AND closed = 0`, sessionID)
	if err != nil {
		return err
	}
	type seg struct {
		id   int64
		path string
	}
	var open []seg
	for rows.Next() {
		var x seg
		if err := rows.Scan(&x.id, &x.path); err != nil {
			rows.Close()
			return err
		}
		open = append(open, x)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, x := range open {
		full, err := s.segmentPath(x.path)
		if err != nil {
			continue
		}
		fi, err := os.Stat(full)
		if err != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE segments SET closed = 1 WHERE id = ?`, x.id)
			continue
		}
		if err := s.sealSegment(ctx, sessionID, x.id, x.path, fi.Size()); err != nil {
			return fmt.Errorf("sealing %s: %w", x.path, err)
		}
	}
	return nil
}

// segmentPath resolves an indexed relative path, refusing anything that
// would leave the sessions directory (the database is not trusted blindly).
func (s *Store) segmentPath(rel string) (string, error) {
	base := sessionsDir(s.dir)
	full := filepath.Join(base, filepath.FromSlash(rel))
	r, err := filepath.Rel(base, full)
	if err != nil || r == ".." || len(r) >= 3 && r[:3] == ".."+string(filepath.Separator) || filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "\\") || filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("segment path %q escapes the sessions directory", rel)
	}
	return full, nil
}
