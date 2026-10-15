package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/appdir"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// runState is the small live-status file a running session keeps so that
// `kestrel status` in another terminal can see it. It holds names, PIDs and
// states only: never commands, environment, or output. Session storage
// proper (SQLite) replaces this in a later phase.
type runState struct {
	SchemaVersion int                 `json:"schema_version"`
	SessionID     string              `json:"session_id"`
	Mode          string              `json:"mode"`
	KestrelPID    int                 `json:"kestrel_pid"`
	ProjectRoot   string              `json:"project_root"`
	StartedAt     time.Time           `json:"started_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
	EndedAt       time.Time           `json:"ended_at,omitzero"`
	Services      []api.ServiceStatus `json:"services"`
}

func statePath(root string) (string, error) {
	dir, _, err := appdir.Resolve()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(dir, "run", hex.EncodeToString(sum[:8])+".json"), nil
}

func readState(root string) (*runState, error) {
	p, err := statePath(root)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var st runState
	if err := json.Unmarshal(b, &st); err != nil || st.ProjectRoot != root {
		return nil, err
	}
	return &st, nil
}

// stateWriter persists runState, coalescing bursts of changes.
type stateWriter struct {
	mu    sync.Mutex
	st    runState
	path  string
	dirty bool
	stop  chan struct{}
	done  chan struct{}
}

func newStateWriter(root, session, mode string, now time.Time) *stateWriter {
	w := &stateWriter{
		st:   runState{SchemaVersion: 1, SessionID: session, Mode: mode, KestrelPID: os.Getpid(), ProjectRoot: root, StartedAt: now},
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	p, err := statePath(root)
	if err != nil {
		slog.Warn("live status file disabled", "err", err)
	}
	w.path = p
	return w
}

func (w *stateWriter) start() {
	go func() {
		defer close(w.done)
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-t.C:
				w.flush(false)
			}
		}
	}()
}

func (w *stateWriter) update(s []api.ServiceStatus) {
	w.mu.Lock()
	w.st.Services, w.dirty = s, true
	w.mu.Unlock()
}

func (w *stateWriter) finish(s []api.ServiceStatus) {
	close(w.stop)
	<-w.done
	w.mu.Lock()
	w.st.Services, w.st.EndedAt, w.dirty = s, time.Now(), true
	w.mu.Unlock()
	w.flush(true)
}

func (w *stateWriter) flush(force bool) {
	w.mu.Lock()
	if w.path == "" || (!w.dirty && !force) {
		w.mu.Unlock()
		return
	}
	w.st.UpdatedAt = time.Now()
	b, err := json.MarshalIndent(w.st, "", "  ")
	w.dirty = false
	w.mu.Unlock()
	if err != nil {
		return
	}
	if err := writeAtomic(w.path, b); err != nil {
		slog.Warn("could not write live status file", "path", w.path, "err", err)
	}
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
