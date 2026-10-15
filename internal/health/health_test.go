package health

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/config"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func TestHTTPProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte("status: healthy"))
		case "/redirect":
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	tests := []struct {
		path string
		lo   int
		hi   int
		body string
		ok   bool
	}{
		{"/ok", 200, 399, "", true},
		{"/ok", 200, 399, "healthy", true},
		{"/ok", 200, 399, "sick", false},
		{"/redirect", 200, 399, "", true}, // 302 is in range and not followed
		{"/redirect", 200, 299, "", false},
		{"/down", 200, 399, "", false},
	}
	for _, tt := range tests {
		p := &HTTP{URL: srv.URL + tt.path, StatusLo: tt.lo, StatusHi: tt.hi, BodyContains: tt.body}
		if r := p.Probe(ctx); r.OK != tt.ok {
			t.Errorf("%s [%d-%d %q]: %+v", tt.path, tt.lo, tt.hi, tt.body, r)
		}
	}
	// Nothing listening.
	if r := (&HTTP{URL: "http://127.0.0.1:1/", StatusLo: 200, StatusHi: 399}).Probe(ctx); r.OK || !strings.Contains(r.Detail, "refused") {
		t.Errorf("closed port: %+v", r)
	}
}

func TestTCPProbe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if r := (&TCP{Address: addr}).Probe(context.Background()); !r.OK {
		t.Fatalf("open port: %+v", r)
	}
	ln.Close()
	if r := (&TCP{Address: addr}).Probe(context.Background()); r.OK {
		t.Fatalf("closed port: %+v", r)
	}
}

func TestFromConfig(t *testing.T) {
	p, err := FromConfig(&config.Health{Type: "http", URL: "http://x/", ExpectStatus: "204"}, nil)
	if err != nil || p.(*HTTP).StatusLo != 204 || p.(*HTTP).StatusHi != 204 {
		t.Fatalf("%+v %v", p, err)
	}
	p, _ = FromConfig(&config.Health{Type: "http", URL: "http://x/"}, nil)
	if h := p.(*HTTP); h.StatusLo != 200 || h.StatusHi != 399 {
		t.Fatalf("default range: %+v", h)
	}
	alive := true
	p, _ = FromConfig(&config.Health{Type: "process"}, func() bool { return alive })
	if !p.Probe(context.Background()).OK {
		t.Fatal("process probe should pass while alive")
	}
	alive = false
	if p.Probe(context.Background()).OK {
		t.Fatal("process probe should fail when not alive")
	}
}

// flaky is a prober whose answer the test controls.
type flaky struct{ ok atomic.Bool }

func (f *flaky) Probe(context.Context) Result {
	if f.ok.Load() {
		return Result{OK: true, Detail: "ok"}
	}
	return Result{Detail: "down"}
}
func (f *flaky) String() string { return "flaky" }

type changes struct {
	mu sync.Mutex
	c  []Change
}

func (c *changes) add(ch Change) { c.mu.Lock(); c.c = append(c.c, ch); c.mu.Unlock() }
func (c *changes) types() []api.EventType {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []api.EventType
	for _, ch := range c.c {
		out = append(out, ch.Type)
	}
	return out
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMonitorLifecycle(t *testing.T) {
	f := &flaky{}
	ch := &changes{}
	m := &Monitor{Service: "api", Prober: f, OnChange: ch.add,
		Policy: Policy{Interval: 10 * time.Millisecond, Timeout: 10 * time.Millisecond, Retries: 3, StartupGrace: 50 * time.Millisecond}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	// Failing during the grace period does not change state.
	time.Sleep(30 * time.Millisecond)
	if s, _, _ := m.State(); s != api.ReadinessStarting {
		t.Fatalf("during grace: %s", s)
	}
	f.ok.Store(true)
	waitFor(t, func() bool { s, _, _ := m.State(); return s == api.ReadinessReady })
	f.ok.Store(false)
	waitFor(t, func() bool { s, _, _ := m.State(); return s == api.ReadinessNotReady })
	f.ok.Store(true)
	waitFor(t, func() bool { s, _, _ := m.State(); return s == api.ReadinessReady })

	got := ch.types()
	want := []api.EventType{api.EventProbeReady, api.EventProbeFailed, api.EventProbeRecovered}
	if len(got) != len(want) {
		t.Fatalf("transitions %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("transitions %v, want %v", got, want)
		}
	}
	if ch.c[1].Fails != 3 {
		t.Errorf("probe_failed after %d fails, want 3", ch.c[1].Fails)
	}
}

func TestMonitorStartupTimeout(t *testing.T) {
	ch := &changes{}
	m := &Monitor{Service: "db", Prober: &flaky{}, OnChange: ch.add,
		Policy: Policy{Interval: 5 * time.Millisecond, Timeout: 5 * time.Millisecond, Retries: 2, StartupGrace: 20 * time.Millisecond}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	waitFor(t, func() bool { s, _, _ := m.State(); return s == api.ReadinessNotReady })
	if got := ch.types(); len(got) != 1 || got[0] != api.EventStartupTimeout {
		t.Fatalf("transitions %v", got)
	}
}
