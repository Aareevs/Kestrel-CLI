package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/Aareevs/Kestrel-CLI/internal/appdir"
	"github.com/Aareevs/Kestrel-CLI/internal/process"
	"github.com/Aareevs/Kestrel-CLI/internal/storage"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// notableCap bounds the events shown by `sessions show`.
const notableCap = 40

// openStore opens the session store in the data directory.
func (a *App) openStore(ctx context.Context) (*storage.Store, error) {
	dir, _, err := appdir.Resolve()
	if err != nil {
		return nil, err
	}
	return storage.Open(ctx, dir, storage.Options{Now: a.Now})
}

// openStoreRecovered opens the store and first marks sessions whose Kestrel
// process is gone as interrupted, so commands never show a dead session as
// active.
func (a *App) openStoreRecovered(ctx context.Context) (*storage.Store, error) {
	st, err := a.openStore(ctx)
	if err != nil {
		return nil, err
	}
	if n, err := st.RecoverStale(ctx, process.Alive); err != nil {
		slog.Warn("could not recover stale sessions", "err", err)
	} else if n > 0 {
		slog.Debug("recovered stale sessions", "count", n)
	}
	return st, nil
}

// policy returns the retention policy from the project's config (when it has
// a valid one) or the defaults.
func (a *App) policy() storage.Policy {
	cfg, _, err := a.LoadConfig()
	if err != nil || cfg == nil {
		return storage.Policy{}
	}
	return storage.Policy{Days: cfg.Storage.RetentionDays, MaxMB: cfg.Storage.MaxTotalMB}
}

// openRecorder starts persisting a session. If storage is unavailable it
// returns a nil recorder after warning: supervision never depends on it.
// closeStore finishes the session, applies retention, and closes the store.
func (a *App) openRecorder(sessionID, mode string, services []string, warn func(string)) (*storage.Recorder, func(status string, final []api.ServiceStatus) *api.RetentionResult) {
	ctx := context.Background()
	st, err := a.openStoreRecovered(ctx)
	if err != nil {
		warn(fmt.Sprintf("session storage unavailable, this session will not be saved: %v", storageHint(err)))
		return nil, nil
	}
	if err := st.CreateSession(ctx, storage.NewSession{
		ID: sessionID, Mode: mode, ProjectRoot: a.ProjectRoot, PID: os.Getpid(), StartedAt: a.Now(), Services: services,
	}); err != nil {
		st.Close()
		warn(fmt.Sprintf("session storage unavailable, this session will not be saved: %v", err))
		return nil, nil
	}
	rec := st.NewRecorder(sessionID)
	closeFn := func(status string, final []api.ServiceStatus) *api.RetentionResult {
		defer st.Close()
		if err := st.FinishSession(ctx, sessionID, status, a.Now(), final); err != nil {
			warn(fmt.Sprintf("could not finalize the saved session: %v", err))
		}
		res, err := st.Enforce(ctx, a.policy(), sessionID, false)
		if err != nil {
			warn(fmt.Sprintf("retention cleanup problem: %v", err))
		}
		if len(res.Removed) == 0 {
			return nil
		}
		return &res
	}
	return rec, closeFn
}

// storageHint adds an actionable pointer to common storage errors.
func storageHint(err error) string {
	switch {
	case errors.Is(err, storage.ErrCorrupt):
		return fmt.Sprintf("%v (run 'kestrel doctor'; the file is left untouched so you can inspect or move it)", err)
	case errors.Is(err, storage.ErrTooNew):
		return err.Error()
	}
	return err.Error() + " (check permissions on the data directory, or set KESTREL_HOME)"
}

// SessionsList lists recorded sessions, for this project or all of them.
func (a *App) SessionsList(ctx context.Context, all bool, limit int) (*api.SessionList, error) {
	st, err := a.openStoreRecovered(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s", storageHint(err))
	}
	defer st.Close()
	f := storage.Filter{Limit: limit}
	if !all {
		f.ProjectRoot = a.ProjectRoot
	}
	list, err := st.ListSessions(ctx, f)
	if err != nil {
		return nil, err
	}
	return &api.SessionList{SchemaVersion: api.SchemaVersion, Sessions: list, AllProjects: all}, nil
}

// resolve finds a session by reference, mapping lookup problems to usage errors.
func (a *App) resolve(ctx context.Context, st *storage.Store, ref string, all bool) (string, error) {
	root := a.ProjectRoot
	if all {
		root = ""
	}
	id, err := st.Resolve(ctx, ref, root)
	var amb *storage.AmbiguousError
	switch {
	case errors.As(err, &amb), errors.Is(err, storage.ErrNotFound):
		hint := ""
		if !all {
			hint = " (searching this project's sessions; pass --all to search every project)"
		}
		return "", &UsageError{Err: fmt.Errorf("%v%s", err, hint)}
	}
	return id, err
}

// SessionShow returns one session in detail.
func (a *App) SessionShow(ctx context.Context, ref string, all bool) (*api.SessionDetail, error) {
	st, err := a.openStoreRecovered(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s", storageHint(err))
	}
	defer st.Close()
	id, err := a.resolve(ctx, st, ref, all)
	if err != nil {
		return nil, err
	}
	sum, services, err := st.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	counts, err := st.CountsByType(ctx, id)
	if err != nil {
		return nil, err
	}
	notable, err := st.Events(ctx, id, storage.EventQuery{Limit: 2000})
	if err != nil {
		return nil, err
	}
	var keep []api.Event
	for _, e := range notable {
		if e.Severity.Rank() >= api.SeverityWarn.Rank() || (e.Type != api.EventLogLine && e.Type != api.EventCollectorOverload) {
			keep = append(keep, e)
		}
	}
	if len(keep) > notableCap {
		keep = keep[len(keep)-notableCap:]
	}
	if keep == nil {
		keep = []api.Event{}
	}
	return &api.SessionDetail{SchemaVersion: api.SchemaVersion, Session: sum, Services: services, CountsByType: counts, Notable: keep, NotableCap: notableCap}, nil
}

// SessionExport writes a redacted JSON bundle of a session to w.
func (a *App) SessionExport(ctx context.Context, ref string, all bool, w io.Writer) (string, error) {
	st, err := a.openStoreRecovered(ctx)
	if err != nil {
		return "", fmt.Errorf("%s", storageHint(err))
	}
	defer st.Close()
	id, err := a.resolve(ctx, st, ref, all)
	if err != nil {
		return "", err
	}
	red, err := a.redactor()
	if err != nil {
		return "", err
	}
	return id, st.Export(ctx, id, red.Redact, w)
}

// SessionsDelete removes sessions by reference. Active sessions are refused.
func (a *App) SessionsDelete(ctx context.Context, refs []string, all bool) ([]api.RemovedSession, error) {
	st, err := a.openStoreRecovered(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s", storageHint(err))
	}
	defer st.Close()
	var ids []string
	for _, ref := range refs {
		id, err := a.resolve(ctx, st, ref, all)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	var out []api.RemovedSession
	for _, id := range ids {
		rem, err := st.DeleteSession(ctx, id)
		if err != nil {
			if strings.Contains(err.Error(), "is active") {
				return out, &UsageError{Err: err}
			}
			return out, err
		}
		out = append(out, rem)
	}
	return out, nil
}

// Cleanup applies the retention policy now.
func (a *App) Cleanup(ctx context.Context, dryRun bool) (*api.RetentionResult, error) {
	st, err := func() (*storage.Store, error) {
		if dryRun { // a dry run must not change anything, not even stale-session state
			return a.openStore(ctx)
		}
		return a.openStoreRecovered(ctx)
	}()
	if err != nil {
		return nil, fmt.Errorf("%s", storageHint(err))
	}
	defer st.Close()
	res, err := st.Enforce(ctx, a.policy(), "", dryRun)
	return &res, err
}
