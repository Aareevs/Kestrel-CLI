package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// ErrNotFound is returned when no session matches a reference.
var ErrNotFound = errors.New("no such session")

// AmbiguousError is returned when a prefix matches several sessions.
type AmbiguousError struct {
	Ref     string
	Matches []string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("session %q is ambiguous (matches %s); use more characters", e.Ref, strings.Join(e.Matches, ", "))
}

// NewSession is what the supervisor knows when a session starts.
type NewSession struct {
	ID          string
	Mode        string
	ProjectRoot string
	PID         int
	StartedAt   time.Time
	Services    []string
}

// CreateSession registers an active session.
func (s *Store) CreateSession(ctx context.Context, n NewSession) error {
	svc, _ := json.Marshal(n.Services)
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (id, mode, project_root, status, kestrel_pid, started_ns, services)
		VALUES (?, ?, ?, 'active', ?, ?, ?)`, n.ID, n.Mode, n.ProjectRoot, n.PID, n.StartedAt.UnixNano(), string(svc))
	return err
}

// FinishSession closes a session and records the final service states.
func (s *Store) FinishSession(ctx context.Context, id, status string, ended time.Time, final []api.ServiceStatus) error {
	fs, _ := json.Marshal(final)
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET status = ?, ended_ns = ?, final_status = ? WHERE id = ?`,
		status, ended.UnixNano(), string(fs), id)
	return err
}

// Filter narrows ListSessions.
type Filter struct {
	ProjectRoot string // "" = all projects
	Limit       int    // 0 = no limit
}

const sessionCols = `id, mode, project_root, status, started_ns, ended_ns, services, events, errors, dropped, bytes, write_errors`

type scanner interface{ Scan(...any) error }

func scanSummary(r scanner) (api.SessionSummary, error) {
	var (
		s        api.SessionSummary
		started  int64
		ended    sql.NullInt64
		services string
	)
	if err := r.Scan(&s.ID, &s.Mode, &s.ProjectRoot, &s.Status, &started, &ended, &services,
		&s.Events, &s.Errors, &s.Dropped, &s.SizeBytes, &s.WriteErrors); err != nil {
		return s, err
	}
	s.StartedAt = time.Unix(0, started).UTC()
	if ended.Valid {
		s.EndedAt = time.Unix(0, ended.Int64).UTC()
	}
	_ = json.Unmarshal([]byte(services), &s.Services)
	if s.Services == nil {
		s.Services = []string{}
	}
	return s, nil
}

// ListSessions returns sessions, newest first.
func (s *Store) ListSessions(ctx context.Context, f Filter) ([]api.SessionSummary, error) {
	q := `SELECT ` + sessionCols + ` FROM sessions`
	var args []any
	if f.ProjectRoot != "" {
		q += ` WHERE project_root = ?`
		args = append(args, f.ProjectRoot)
	}
	q += ` ORDER BY started_ns DESC, id DESC`
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.SessionSummary{}
	for rows.Next() {
		sm, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}

// Resolve turns a reference into a session ID. ref may be a full ID, a unique
// prefix (case-insensitive), or "last"/"latest". All forms are limited to
// projectRoot's sessions, or span every project when projectRoot is empty.
func (s *Store) Resolve(ctx context.Context, ref, projectRoot string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.EqualFold(ref, "last") || strings.EqualFold(ref, "latest") {
		list, err := s.ListSessions(ctx, Filter{ProjectRoot: projectRoot, Limit: 1})
		if err != nil {
			return "", err
		}
		if len(list) == 0 {
			return "", fmt.Errorf("%w: none recorded yet", ErrNotFound)
		}
		return list[0].ID, nil
	}
	if strings.ContainsAny(ref, "%_\\") {
		return "", fmt.Errorf("%w: %q", ErrNotFound, ref)
	}
	q := `SELECT id FROM sessions WHERE id LIKE ? ESCAPE '\'`
	args := []any{strings.ToUpper(ref) + "%"}
	if projectRoot != "" { // IDs and prefixes are scoped like "last": --all widens them
		q += ` AND project_root = ?`
		args = append(args, projectRoot)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY id LIMIT 6`, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	switch {
	case len(ids) == 0:
		return "", fmt.Errorf("%w: %q", ErrNotFound, ref)
	case len(ids) > 1:
		for _, id := range ids {
			if id == strings.ToUpper(ref) {
				return id, nil
			}
		}
		return "", &AmbiguousError{Ref: ref, Matches: ids}
	}
	return ids[0], rows.Err()
}

// GetSession returns the summary and final service states.
func (s *Store) GetSession(ctx context.Context, id string) (api.SessionSummary, []api.ServiceStatus, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+sessionCols+`, final_status FROM sessions WHERE id = ?`, id)
	var (
		sm       api.SessionSummary
		started  int64
		ended    sql.NullInt64
		services string
		final    string
	)
	err := row.Scan(&sm.ID, &sm.Mode, &sm.ProjectRoot, &sm.Status, &started, &ended, &services,
		&sm.Events, &sm.Errors, &sm.Dropped, &sm.SizeBytes, &sm.WriteErrors, &final)
	if errors.Is(err, sql.ErrNoRows) {
		return sm, nil, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if err != nil {
		return sm, nil, err
	}
	sm.StartedAt = time.Unix(0, started).UTC()
	if ended.Valid {
		sm.EndedAt = time.Unix(0, ended.Int64).UTC()
	}
	_ = json.Unmarshal([]byte(services), &sm.Services)
	var st []api.ServiceStatus
	_ = json.Unmarshal([]byte(final), &st)
	if st == nil {
		st = []api.ServiceStatus{}
	}
	return sm, st, nil
}

// EventQuery selects events from one session in seq order.
type EventQuery struct {
	Service     string
	Types       []api.EventType
	MinSeverity api.Severity
	AfterSeq    uint64
	Limit       int // 0 = no limit
	// Newest returns the last Limit matching events (still in seq order).
	Newest bool
}

// Events reads indexed events from SQLite. Messages may be truncated; use
// ExportEvents for full text.
func (s *Store) Events(ctx context.Context, sessionID string, q EventQuery) ([]api.Event, error) {
	where := []string{"session_id = ?", "seq > ?"}
	args := []any{sessionID, q.AfterSeq}
	if q.Service != "" {
		where = append(where, "service = ?")
		args = append(args, q.Service)
	}
	if len(q.Types) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(q.Types)), ",")
		where = append(where, "type IN ("+ph+")")
		for _, t := range q.Types {
			args = append(args, string(t))
		}
	}
	if q.MinSeverity != "" {
		var sevs []string
		for _, sv := range []api.Severity{api.SeverityDebug, api.SeverityInfo, api.SeverityWarn, api.SeverityError, api.SeverityFatal} {
			if sv.Rank() >= q.MinSeverity.Rank() {
				sevs = append(sevs, "'"+string(sv)+"'")
			}
		}
		where = append(where, "severity IN ("+strings.Join(sevs, ",")+")")
	}
	order := "ASC"
	if q.Newest {
		order = "DESC"
	}
	query := `SELECT seq, id, time_ns, service, pid, type, severity, source, message, attrs, fingerprint
		FROM events WHERE ` + strings.Join(where, " AND ") + ` ORDER BY seq ` + order
	if q.Limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, q.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.Event{}
	for rows.Next() {
		var (
			e     api.Event
			t     int64
			attrs string
			typ   string
			sev   string
			src   string
		)
		if err := rows.Scan(&e.Seq, &e.ID, &t, &e.ServiceID, &e.PID, &typ, &sev, &src, &e.Message, &attrs, &e.Fingerprint); err != nil {
			return nil, err
		}
		e.SessionID, e.Time = sessionID, time.Unix(0, t).UTC()
		e.Type, e.Severity, e.Source = api.EventType(typ), api.Severity(sev), api.EventSource(src)
		_ = json.Unmarshal([]byte(attrs), &e.Attrs)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if q.Newest { // restore seq order
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, nil
}

// CountsByType returns how many events of each type a session has.
func (s *Store) CountsByType(ctx context.Context, sessionID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT type, COUNT(*) FROM events WHERE session_id = ? GROUP BY type`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, rows.Err()
}

// DeleteSession removes a session's rows (cascading to events and segment
// index) and then its segment files. It refuses active sessions.
func (s *Store) DeleteSession(ctx context.Context, id string) (api.RemovedSession, error) {
	rem := api.RemovedSession{ID: id, Reason: "requested"}
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status, bytes FROM sessions WHERE id = ?`, id).Scan(&status, &rem.SizeBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return rem, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if err != nil {
		return rem, err
	}
	if status == api.SessionActive {
		return rem, fmt.Errorf("session %s is active; stop it first", id)
	}
	return rem, s.removeSession(ctx, id)
}

func (s *Store) removeSession(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return err
	}
	// Rows are gone first: a leftover directory is harmless garbage, while a
	// row pointing at deleted files would be a lie.
	if err := os.RemoveAll(filepath.Join(sessionsDir(s.dir), id)); err != nil {
		return fmt.Errorf("session %s removed from the database, but its files could not be deleted: %w", id, err)
	}
	return nil
}
