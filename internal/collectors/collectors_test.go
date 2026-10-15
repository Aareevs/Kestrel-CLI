package collectors

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/events"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func TestReadLines(t *testing.T) {
	long := strings.Repeat("x", 100)
	in := "first\r\n" +
		"\x1b[32mgreen\x1b[0m text\n" +
		"progress 10%\rprogress 50%\rprogress 100%\n" +
		long + "\n" +
		"bad \xff utf8\n" +
		"no newline at end"
	type got struct {
		line  string
		trunc int
	}
	var lines []got
	if err := ReadLines(strings.NewReader(in), 64, func(l string, n int) { lines = append(lines, got{l, n}) }); err != nil {
		t.Fatal(err)
	}
	want := []got{
		{"first", 0},
		{"green text", 0},
		{"progress 100%", 0},
		{strings.Repeat("x", 64), 36},
		{"bad � utf8", 0},
		{"no newline at end", 0},
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines: %+v", len(lines), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, lines[i], want[i])
		}
	}
}

func TestReadLinesHugeLineWithoutNewline(t *testing.T) {
	var lines []string
	var trunc int
	err := ReadLines(strings.NewReader(strings.Repeat("y", 10000)), 64, func(l string, n int) {
		lines = append(lines, l)
		trunc += n
	})
	if err != nil || len(lines) != 1 || len(lines[0]) != 64 || trunc != 10000-64 {
		t.Fatalf("err=%v lines=%d len=%d trunc=%d", err, len(lines), len(lines[0]), trunc)
	}
}

func ev(seq uint64, sev api.Severity, typ api.EventType) api.Event {
	return api.Event{Seq: seq, Severity: sev, Type: typ, Message: fmt.Sprint(seq)}
}

func TestBufferDropsOldestInfoFirst(t *testing.T) {
	b := NewBuffer(3, 1<<20)
	b.Push(ev(1, api.SeverityInfo, api.EventLogLine))
	b.Push(ev(2, api.SeverityError, api.EventLogLine))
	b.Push(ev(3, api.SeverityInfo, api.EventLogLine))
	b.Push(ev(4, api.SeverityInfo, api.EventLogLine))  // evicts 1
	b.Push(ev(5, api.SeverityDebug, api.EventLogLine)) // evicts 3
	b.Close()
	var seqs []uint64
	total := 0
	for {
		e, d, ok := b.Pop()
		total += d
		if !ok {
			break
		}
		seqs = append(seqs, e.Seq)
	}
	if fmt.Sprint(seqs) != "[2 4 5]" || total != 2 {
		t.Fatalf("got %v, dropped %d", seqs, total)
	}
}

func TestBufferNeverDropsProtected(t *testing.T) {
	b := NewBuffer(2, 1<<20)
	b.Push(ev(1, api.SeverityError, api.EventLogLine))
	b.Push(ev(2, api.SeverityInfo, api.EventProcessExited)) // lifecycle: protected despite info
	// Full of protected events: an info line is dropped itself...
	b.Push(ev(3, api.SeverityInfo, api.EventLogLine))
	// ...and a protected event blocks until a consumer makes room.
	pushed := make(chan struct{})
	go func() {
		b.Push(ev(4, api.SeverityFatal, api.EventLogLine))
		close(pushed)
	}()
	select {
	case <-pushed:
		t.Fatal("protected push should block while the buffer is full of protected events")
	case <-time.After(50 * time.Millisecond):
	}
	e, dropped, _ := b.Pop()
	if e.Seq != 1 || dropped != 1 {
		t.Fatalf("pop = %d, dropped %d", e.Seq, dropped)
	}
	<-pushed
	b.Close()
	var seqs []uint64
	for {
		e, _, ok := b.Pop()
		if !ok {
			break
		}
		seqs = append(seqs, e.Seq)
	}
	if fmt.Sprint(seqs) != "[2 4]" {
		t.Fatalf("remaining %v", seqs)
	}
}

func TestBufferCloseUnblocksProducer(t *testing.T) {
	b := NewBuffer(1, 1<<20)
	b.Push(ev(1, api.SeverityError, api.EventLogLine))
	errc := make(chan error)
	go func() { errc <- b.Push(ev(2, api.SeverityError, api.EventLogLine)) }()
	time.Sleep(20 * time.Millisecond)
	b.Close()
	if err := <-errc; err != ErrClosed {
		t.Fatalf("got %v", err)
	}
}

func TestBufferByteBound(t *testing.T) {
	b := NewBuffer(1000, 2000)
	big := strings.Repeat("z", 900)
	for i := 1; i <= 10; i++ {
		e := ev(uint64(i), api.SeverityInfo, api.EventLogLine)
		e.Message = big
		b.Push(e)
	}
	n, bytes, dropped := b.Stats()
	if bytes > 2000 || n != 1 || dropped != 9 {
		t.Fatalf("events=%d bytes=%d dropped=%d", n, bytes, dropped)
	}
}

// collect runs a collector and returns everything its sink received.
type recorder struct {
	mu   sync.Mutex
	evs  []api.Event
	gate chan struct{} // if set, Emit blocks until it is closed
}

func (r *recorder) Emit(e api.Event) {
	if r.gate != nil {
		<-r.gate
	}
	r.mu.Lock()
	r.evs = append(r.evs, e)
	r.mu.Unlock()
}

func newTestCollector(rec *recorder, o Options) (*Collector, *events.Stamper) {
	st := events.NewStamper(nil)
	c := New(st, rec, o)
	go c.Run()
	return c, st
}

func TestCollectorClassifiesRedactsAndOrders(t *testing.T) {
	rec := &recorder{}
	c, st := newTestCollector(rec, Options{})
	input := strings.Join([]string{
		"starting api",
		"connecting to postgres://app:hunter2@localhost:5432/orders",
		"dial tcp 127.0.0.1:5432: connect: connection refused",
		"Listening on :8080",
	}, "\n") + "\n"
	if err := c.ReadStream("api", 42, api.SourceStderr, strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	c.Close()
	c.Wait()
	if len(rec.evs) != 4 {
		t.Fatalf("got %d events", len(rec.evs))
	}
	for i, e := range rec.evs {
		if e.Seq != uint64(i+1) || e.SessionID != st.SessionID || e.ServiceID != "api" || e.PID != 42 || e.Source != api.SourceStderr || len(e.ID) != 26 {
			t.Errorf("event %d identity: %+v", i, e)
		}
	}
	if strings.Contains(rec.evs[1].Message, "hunter2") {
		t.Errorf("secret not redacted: %q", rec.evs[1].Message)
	}
	if e := rec.evs[2]; e.Type != api.EventDependencyConnFailed || e.Attrs.Port != 5432 || e.Attrs.PatternID != "P006-connection-refused" {
		t.Errorf("classification: %+v", e)
	}
	if e := rec.evs[3]; e.Attrs.Port != 8080 || e.Attrs.PatternID != "L001-listening" {
		t.Errorf("listen detection: %+v", e)
	}
}

func TestCollectorFoldsRepeats(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestCollector(rec, Options{RepeatWindow: time.Minute, RepeatIdle: time.Hour})
	var b strings.Builder
	b.WriteString("boot\n")
	for i := 1; i <= 14; i++ {
		fmt.Fprintf(&b, "retry %d: dial tcp 127.0.0.1:5432: connect: connection refused\n", i)
	}
	b.WriteString("giving up\n")
	c.ReadStream("api", 1, api.SourceStdout, strings.NewReader(b.String()))
	c.Close()
	c.Wait()
	var msgs []string
	for _, e := range rec.evs {
		msgs = append(msgs, fmt.Sprintf("%s ×%d", e.Message, e.Attrs.Repeats))
	}
	want := []string{
		"boot ×0",
		"retry 1: dial tcp 127.0.0.1:5432: connect: connection refused ×0",
		"retry 1: dial tcp 127.0.0.1:5432: connect: connection refused ×13",
		"giving up ×0",
	}
	if strings.Join(msgs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s", strings.Join(msgs, "\n"))
	}
	if rec.evs[2].Type != api.EventDependencyConnFailed || rec.evs[2].Severity != api.SeverityError {
		t.Fatalf("repeat summary must keep type/severity: %+v", rec.evs[2])
	}
}

func TestCollectorFlushesIdleRepeats(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestCollector(rec, Options{RepeatIdle: 40 * time.Millisecond})
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() { c.ReadStream("x", 1, api.SourceStdout, pr); close(done) }()
	for i := 0; i < 5; i++ {
		fmt.Fprintln(pw, "same line")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		rec.mu.Lock()
		n := len(rec.evs)
		rec.mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("summary not flushed while stream idle; got %d events", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	pw.Close()
	<-done
	c.Close()
	c.Wait()
	if rec.evs[1].Attrs.Repeats != 4 {
		t.Fatalf("repeats = %d", rec.evs[1].Attrs.Repeats)
	}
}

// TestCollectorFloodWithSlowSink is the backpressure contract: a sink that
// can't keep up causes info lines to be dropped and reported, while every
// error line still arrives, in order. The sink is held shut until all input
// has been read, so the buffer is guaranteed to overflow.
func TestCollectorFloodWithSlowSink(t *testing.T) {
	rec := &recorder{gate: make(chan struct{})}
	c, _ := newTestCollector(rec, Options{MaxEvents: 300, MaxBytes: 1 << 20})
	var wg sync.WaitGroup
	const perStream, errEvery = 5000, 100
	for _, name := range []string{"a", "b", "c"} {
		pr, pw := io.Pipe()
		wg.Add(2)
		go func() { defer wg.Done(); c.ReadStream(name, 1, api.SourceStdout, pr) }()
		go func() {
			defer wg.Done()
			for i := 0; i < perStream; i++ {
				if i%errEvery == 0 {
					fmt.Fprintf(pw, "ERROR failure number %d in %s\n", i, name)
				} else {
					// Rotate words so consecutive lines differ and are not folded as repeats.
					fmt.Fprintf(pw, "info %s line %d\n", []string{"alpha", "beta", "gamma", "delta"}[i%4], i)
				}
			}
			pw.Close()
		}()
	}
	wg.Wait()
	close(rec.gate)
	c.Close()
	c.Wait()

	errors, overload, dropped := 0, 0, 0
	var last uint64
	for _, e := range rec.evs {
		if e.Severity == api.SeverityError {
			errors++
		}
		if e.Type == api.EventCollectorOverload {
			overload++
			dropped += e.Attrs.Dropped
		} else if e.Seq <= last {
			t.Fatalf("out of order: %d after %d", e.Seq, last)
		} else {
			last = e.Seq
		}
	}
	if want := 3 * perStream / errEvery; errors != want {
		t.Fatalf("error lines delivered = %d, want all %d", errors, want)
	}
	if overload == 0 || dropped == 0 {
		t.Fatalf("expected drops to be reported (overload events=%d, dropped=%d)", overload, dropped)
	}
	_, _, total := c.Stats()
	if total != dropped {
		t.Fatalf("reported drops %d != counted drops %d", dropped, total)
	}
	t.Logf("delivered %d events, dropped %d info lines in %d overload reports", len(rec.evs)-overload, dropped, overload)
}
