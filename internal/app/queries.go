package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Aareevs/Kestrel-CLI/internal/storage"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Read-only queries used by the MCP server. Like everything in this package,
// they return data that was redacted before it was stored.

// Services lists the project's services: from kestrel.yaml (commands masked),
// else from the latest recorded session's topology.
func (a *App) Services(ctx context.Context) (*api.ServiceList, error) {
	out := &api.ServiceList{SchemaVersion: api.SchemaVersion, Services: []api.ServiceInfo{}}
	cfg, _, err := a.LoadConfig()
	switch {
	case err == nil:
		out.Source = "kestrel.yaml"
		for _, s := range cfg.Masked().Services {
			si := api.ServiceInfo{Name: s.Name, Command: s.Command, Dir: s.Dir, DependsOn: s.DependsOn, External: s.Command == ""}
			if h := s.Health; h != nil {
				si.Probe = strings.TrimSpace(h.Type + " " + h.URL + h.Address)
			}
			out.Services = append(out.Services, si)
		}
		return out, nil
	case !errors.Is(err, ErrNoConfig):
		return nil, &UsageError{Err: err}
	}
	st, err := a.openStoreRecovered(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s", storageHint(err))
	}
	defer st.Close()
	id, err := st.Resolve(ctx, "last", a.ProjectRoot)
	if errors.Is(err, storage.ErrNotFound) {
		out.Source = "none"
		out.Note = "no kestrel.yaml and no recorded session; inspect_project shows what discovery finds"
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	topo, err := st.Topology(ctx, id)
	if err != nil {
		return nil, err
	}
	out.Source = "session " + id
	out.Note = "no kestrel.yaml; these are the services the latest recorded session ran"
	for _, s := range topo.Services {
		out.Services = append(out.Services, api.ServiceInfo{Name: s.Name, DependsOn: s.DependsOn, External: s.External})
	}
	return out, nil
}

// RecentErrors returns a session's latest error and fatal events (all
// services, or one), oldest first.
func (a *App) RecentErrors(ctx context.Context, ref, service string, limit int) (*api.RecentErrors, error) {
	rep, err := a.recentErrors(ctx, ref, service, limit)
	return scrubbed(a, rep, err)
}

func (a *App) recentErrors(ctx context.Context, ref, service string, limit int) (*api.RecentErrors, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	st, err := a.openStoreRecovered(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s", storageHint(err))
	}
	defer st.Close()
	id, err := a.resolve(ctx, st, ref, false)
	if err != nil {
		return nil, err
	}
	sum, _, err := st.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	evs, err := st.Events(ctx, id, storage.EventQuery{Service: service, MinSeverity: api.SeverityError, Limit: limit, Newest: true})
	if err != nil {
		return nil, err
	}
	return &api.RecentErrors{SchemaVersion: api.SchemaVersion, Session: sum, Service: service, Events: evs, Limit: limit}, nil
}

// VerificationResults lists a session's verifications, with the latest
// report in full.
func (a *App) VerificationResults(ctx context.Context, ref string) (*api.VerificationResults, error) {
	rep, err := a.verificationResults(ctx, ref)
	return scrubbed(a, rep, err)
}

func (a *App) verificationResults(ctx context.Context, ref string) (*api.VerificationResults, error) {
	st, err := a.openStoreRecovered(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s", storageHint(err))
	}
	defer st.Close()
	id, err := a.resolve(ctx, st, ref, false)
	if err != nil {
		return nil, err
	}
	sum, _, err := st.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	vs, err := st.Verifications(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &api.VerificationResults{SchemaVersion: api.SchemaVersion, Session: sum, Verifications: vs}
	if len(vs) > 0 {
		if out.Latest, err = st.Verification(ctx, vs[len(vs)-1].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SearchOptions select events for SearchEvents.
type SearchOptions struct {
	Session     string
	Query       string // text to find in the message (ASCII case-insensitive)
	Service     string
	Type        api.EventType
	MinSeverity api.Severity
	Limit       int
}

// Search bounds.
const (
	maxSearchQuery = 200
	maxSearchLimit = 200
	defSearchLimit = 50
)

// SearchEvents finds events in one session whose message contains Query. It
// reads recorded (already redacted) events only.
func (a *App) SearchEvents(ctx context.Context, o SearchOptions) (*api.EventSearch, error) {
	rep, err := a.searchEvents(ctx, o)
	return scrubbed(a, rep, err)
}

func (a *App) searchEvents(ctx context.Context, o SearchOptions) (*api.EventSearch, error) {
	q := strings.TrimSpace(o.Query)
	switch {
	case q == "":
		return nil, &UsageError{Err: fmt.Errorf("query is required")}
	case len(q) > maxSearchQuery:
		return nil, &UsageError{Err: fmt.Errorf("query is %d bytes; the limit is %d", len(q), maxSearchQuery)}
	}
	if o.MinSeverity != "" && o.MinSeverity.Rank() == 0 {
		return nil, &UsageError{Err: fmt.Errorf("unknown severity %q", o.MinSeverity)}
	}
	limit := o.Limit
	if limit <= 0 || limit > maxSearchLimit {
		limit = defSearchLimit
	}
	st, err := a.openStoreRecovered(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s", storageHint(err))
	}
	defer st.Close()
	id, err := a.resolve(ctx, st, o.Session, false)
	if err != nil {
		return nil, err
	}
	sum, _, err := st.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	eq := storage.EventQuery{Service: o.Service, MinSeverity: o.MinSeverity, Contains: q, Limit: limit + 1, Newest: true}
	if o.Type != "" {
		eq.Types = []api.EventType{o.Type}
	}
	evs, err := st.Events(ctx, id, eq)
	if err != nil {
		return nil, err
	}
	out := &api.EventSearch{SchemaVersion: api.SchemaVersion, Session: sum, Query: q, Limit: limit}
	if len(evs) > limit { // the newest limit+1, in order: drop the oldest
		out.Truncated = true
		evs = evs[len(evs)-limit:]
	}
	out.Events = evs
	return out, nil
}
