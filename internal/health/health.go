// Package health probes services and tracks readiness.
//
// Liveness (is the process running?) comes from the supervisor. Readiness
// (does the service answer?) comes from probes. Kestrel reports both.
package health

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/config"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Result is the outcome of one probe.
type Result struct {
	OK         bool
	Detail     string
	HTTPStatus int
	Latency    time.Duration
}

// Prober performs one check. Implementations must honor ctx's deadline.
type Prober interface {
	Probe(ctx context.Context) Result
	String() string
}

// HTTP checks that a URL answers with an expected status and, optionally, a
// body substring. Redirects are not followed: a 3xx is itself an answer.
type HTTP struct {
	URL          string
	StatusLo     int
	StatusHi     int
	BodyContains string
	Client       *http.Client
}

func (h *HTTP) String() string { return "http " + h.URL }

func (h *HTTP) Probe(ctx context.Context) Result {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.URL, nil)
	if err != nil {
		return Result{Detail: err.Error()}
	}
	req.Header.Set("User-Agent", "kestrel-health")
	client := h.Client
	if client == nil {
		client = defaultClient
	}
	resp, err := client.Do(req)
	lat := time.Since(start)
	if err != nil {
		return Result{Detail: shortNetErr(err), Latency: lat}
	}
	defer resp.Body.Close()
	r := Result{HTTPStatus: resp.StatusCode, Latency: lat}
	if resp.StatusCode < h.StatusLo || resp.StatusCode > h.StatusHi {
		r.Detail = fmt.Sprintf("status %d (want %d-%d)", resp.StatusCode, h.StatusLo, h.StatusHi)
		return r
	}
	if h.BodyContains != "" {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if !strings.Contains(string(body), h.BodyContains) {
			r.Detail = fmt.Sprintf("status %d but body lacks %q", resp.StatusCode, h.BodyContains)
			return r
		}
	}
	r.OK = true
	r.Detail = fmt.Sprintf("status %d in %s", resp.StatusCode, lat.Round(time.Millisecond))
	return r
}

var defaultClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Transport: &http.Transport{
		Proxy:             nil, // local probes never go through a proxy
		DisableKeepAlives: true,
	},
}

// TCP checks that something accepts connections on an address.
type TCP struct{ Address string }

func (t *TCP) String() string { return "tcp " + t.Address }

func (t *TCP) Probe(ctx context.Context) Result {
	start := time.Now()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", t.Address)
	lat := time.Since(start)
	if err != nil {
		return Result{Detail: shortNetErr(err), Latency: lat}
	}
	conn.Close()
	return Result{OK: true, Detail: "connected in " + lat.Round(time.Millisecond).String(), Latency: lat}
}

// Liveness passes while a process is running.
type Liveness struct{ Alive func() bool }

func (l *Liveness) String() string { return "process" }

func (l *Liveness) Probe(context.Context) Result {
	if l.Alive != nil && l.Alive() {
		return Result{OK: true, Detail: "process running"}
	}
	return Result{Detail: "process not running"}
}

func shortNetErr(err error) string {
	var ne *net.OpError
	if errors.As(err, &ne) && ne.Err != nil {
		return ne.Op + " " + ne.Addr.String() + ": " + ne.Err.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	return err.Error()
}

// FromConfig builds a prober from a validated health config. alive is used
// by process probes.
func FromConfig(h *config.Health, alive func() bool) (Prober, error) {
	switch h.Type {
	case config.HealthHTTP:
		lo, hi, err := parseStatus(h.ExpectStatus)
		if err != nil {
			return nil, err
		}
		return &HTTP{URL: h.URL, StatusLo: lo, StatusHi: hi, BodyContains: h.BodyContains}, nil
	case config.HealthTCP:
		return &TCP{Address: h.Address}, nil
	case config.HealthProcess:
		return &Liveness{Alive: alive}, nil
	}
	return nil, fmt.Errorf("unknown probe type %q", h.Type)
}

func parseStatus(s string) (lo, hi int, err error) {
	if s == "" {
		s = config.DefaultHTTPExpectStatus
	}
	a, b, isRange := strings.Cut(s, "-")
	if lo, err = strconv.Atoi(a); err != nil {
		return 0, 0, fmt.Errorf("expect_status %q: %w", s, err)
	}
	hi = lo
	if isRange {
		if hi, err = strconv.Atoi(b); err != nil {
			return 0, 0, fmt.Errorf("expect_status %q: %w", s, err)
		}
	}
	return lo, hi, nil
}

// Policy controls a Monitor.
type Policy struct {
	Interval     time.Duration
	Timeout      time.Duration
	Retries      int           // consecutive failures before not_ready
	StartupGrace time.Duration // failures in this window don't count
}

// PolicyFromConfig converts config durations, applying defaults.
func PolicyFromConfig(h *config.Health) Policy {
	p := Policy{Interval: h.Interval.D(), Timeout: h.Timeout.D(), Retries: h.Retries, StartupGrace: h.StartupGrace.D()}
	if p.Interval <= 0 {
		p.Interval = config.DefaultHealthInterval
	}
	if p.Timeout <= 0 {
		p.Timeout = config.DefaultHealthTimeout
	}
	if p.Retries <= 0 {
		p.Retries = config.DefaultHealthRetries
	}
	return p
}

// Change is a readiness transition worth recording as an event.
type Change struct {
	Service string
	From    api.Readiness
	To      api.Readiness
	Type    api.EventType // probe_ready, probe_failed, probe_recovered, startup_timeout
	Result  Result
	Fails   int
}

// Monitor runs a prober on a schedule and tracks readiness:
//
//	starting --pass--> ready --Retries consecutive fails--> not_ready --pass--> ready
//	starting --Retries consecutive fails after StartupGrace--> not_ready (startup_timeout)
type Monitor struct {
	Service string
	Prober  Prober
	Policy  Policy
	// OnChange is called synchronously from Run for every transition.
	OnChange func(Change)

	mu    sync.Mutex
	state api.Readiness
	last  Result
	since time.Time
}

// State returns the current readiness, the latest result, and when the state began.
func (m *Monitor) State() (api.Readiness, Result, time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == "" {
		return api.ReadinessStarting, m.last, m.since
	}
	return m.state, m.last, m.since
}

// Run probes until ctx is done.
func (m *Monitor) Run(ctx context.Context) {
	start := time.Now()
	m.mu.Lock()
	m.state, m.since = api.ReadinessStarting, start
	m.mu.Unlock()
	fails := 0
	t := time.NewTicker(m.Policy.Interval)
	defer t.Stop()
	for {
		pctx, cancel := context.WithTimeout(ctx, m.Policy.Timeout)
		r := m.Prober.Probe(pctx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		m.last = r
		from := m.state
		var ch *Change
		if r.OK {
			fails = 0
			if from != api.ReadinessReady {
				typ := api.EventProbeReady
				if from == api.ReadinessNotReady {
					typ = api.EventProbeRecovered
				}
				ch = &Change{From: from, To: api.ReadinessReady, Type: typ}
			}
		} else if time.Since(start) >= m.Policy.StartupGrace || from == api.ReadinessReady {
			fails++
			if fails >= m.Policy.Retries && from != api.ReadinessNotReady {
				typ := api.EventProbeFailed
				if from == api.ReadinessStarting {
					typ = api.EventStartupTimeout
				}
				ch = &Change{From: from, To: api.ReadinessNotReady, Type: typ}
			}
		}
		if ch != nil {
			m.state, m.since = ch.To, time.Now()
			ch.Service, ch.Result, ch.Fails = m.Service, r, fails
		}
		m.mu.Unlock()
		if ch != nil && m.OnChange != nil {
			m.OnChange(*ch)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Once runs a single probe with the policy timeout (used by `kestrel status`).
func Once(ctx context.Context, p Prober, timeout time.Duration) Result {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return p.Probe(ctx)
}
