package events

import (
	"sync/atomic"
	"time"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Stamper assigns session-wide identity to events: ULID, monotonic Seq, and
// capture time. Seq is the authoritative order; wall-clock time is not.
type Stamper struct {
	SessionID string
	Now       func() time.Time
	seq       atomic.Uint64
	ids       IDGen
}

// NewStamper starts a session with a fresh ID.
func NewStamper(now func() time.Time) *Stamper {
	if now == nil {
		now = time.Now
	}
	s := &Stamper{Now: now}
	s.SessionID = s.ids.New(now())
	return s
}

// Stamp fills ID, SessionID, Seq, Time (if unset), and Fingerprint (if unset).
func (s *Stamper) Stamp(ev *api.Event) {
	if ev.Time.IsZero() {
		ev.Time = s.Now()
	}
	ev.SessionID = s.SessionID
	ev.Seq = s.seq.Add(1)
	ev.ID = s.ids.New(ev.Time)
	if ev.Fingerprint == "" && ev.Message != "" {
		ev.Fingerprint = Fingerprint(ev.Message)
	}
}
