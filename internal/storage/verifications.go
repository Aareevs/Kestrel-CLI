package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// SaveVerification stores a verification report against its session. The
// report must already be redacted.
func (s *Store) SaveVerification(ctx context.Context, r *api.VerificationReport) error {
	if r.Session == nil {
		return errors.New("storage: a verification needs a session")
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	inc := 0
	if r.Incident != nil {
		inc = r.Incident.Number
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO verifications (id, session_id, started_ns, ended_ns, verdict, incident, window_len, report)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, r.ID, r.Session.ID, r.StartedAt.UnixNano(), r.EndedAt.UnixNano(), string(r.Verdict), inc, r.Window, string(b))
	return classify(err)
}

// Verifications lists a session's verifications, oldest first.
func (s *Store) Verifications(ctx context.Context, sessionID string) ([]api.VerificationSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, started_ns, ended_ns, verdict, incident, window_len
		FROM verifications WHERE session_id = ? ORDER BY started_ns, id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.VerificationSummary{}
	for rows.Next() {
		var (
			v          api.VerificationSummary
			start, end int64
			verdict    string
		)
		if err := rows.Scan(&v.ID, &start, &end, &verdict, &v.Incident, &v.Window); err != nil {
			return nil, err
		}
		v.SessionID, v.Verdict = sessionID, api.Verdict(verdict)
		v.StartedAt, v.EndedAt = time.Unix(0, start).UTC(), time.Unix(0, end).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}

// Verification returns one stored report by ID.
func (s *Store) Verification(ctx context.Context, id string) (*api.VerificationReport, error) {
	var b string
	err := s.db.QueryRowContext(ctx, `SELECT report FROM verifications WHERE id = ?`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no verification %q", id)
	}
	if err != nil {
		return nil, err
	}
	var r api.VerificationReport
	if err := json.Unmarshal([]byte(b), &r); err != nil {
		return nil, fmt.Errorf("verification %s: %w", id, err)
	}
	return &r, nil
}
