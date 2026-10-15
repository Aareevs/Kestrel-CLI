package collectors

import (
	"io"
	"sync"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/events"
	"github.com/Aareevs/Kestrel-CLI/internal/privacy"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Sink receives events in Seq order from a single goroutine. A slow sink
// fills the buffer, which triggers the drop policy; it never loses protected
// events.
type Sink interface {
	Emit(api.Event)
}

// SinkFunc adapts a function to Sink.
type SinkFunc func(api.Event)

func (f SinkFunc) Emit(ev api.Event) { f(ev) }

// DefaultRepeatWindow is the longest run of identical lines folded into one
// summary event: a steadily repeating error produces an event at least this
// often.
const DefaultRepeatWindow = 10 * time.Second

// Options configure a Collector. Zero values take defaults.
type Options struct {
	MaxLine      int           // longest line kept intact (default 16 KiB)
	MaxEvents    int           // buffer bound in events (default 10000)
	MaxBytes     int           // buffer bound in approximate bytes (default 8 MiB)
	RepeatWindow time.Duration // longest run of repeats folded into one summary (default 10s)
	RepeatIdle   time.Duration // flush a repeat summary after this much quiet (default 1s)
	Redactor     *privacy.Redactor
}

func (o *Options) defaults() {
	if o.MaxLine <= 0 {
		o.MaxLine = DefaultMaxLine
	}
	if o.MaxEvents <= 0 {
		o.MaxEvents = 10000
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = 8 << 20
	}
	if o.RepeatWindow <= 0 {
		o.RepeatWindow = DefaultRepeatWindow
	}
	if o.RepeatIdle <= 0 {
		o.RepeatIdle = time.Second
	}
	if o.Redactor == nil {
		o.Redactor = privacy.Default
	}
}

// Collector gathers events from every stream of a session into one bounded
// buffer and delivers them, in order, to a sink.
type Collector struct {
	opts    Options
	stamper *events.Stamper
	buf     *Buffer
	sink    Sink
	done    chan struct{}
}

// New creates a collector. Call Run in its own goroutine.
func New(stamper *events.Stamper, sink Sink, opts Options) *Collector {
	opts.defaults()
	return &Collector{
		opts:    opts,
		stamper: stamper,
		buf:     NewBuffer(opts.MaxEvents, opts.MaxBytes, stamper.Stamp, overloadEvent),
		sink:    sink,
		done:    make(chan struct{}),
	}
}

// Run delivers events to the sink, in Seq order, until Close has been called
// and the buffer is drained. Drops arrive as collector_overload events.
func (c *Collector) Run() {
	defer close(c.done)
	for {
		ev, ok := c.buf.Pop()
		if !ok {
			return
		}
		c.sink.Emit(ev)
	}
}

func overloadEvent() api.Event {
	return api.Event{
		Type:     api.EventCollectorOverload,
		Severity: api.SeverityWarn,
		Source:   api.SourceKestrel,
		Message:  "output arrived faster than it could be processed; oldest debug/info lines were dropped (errors and lifecycle events are never dropped)",
	}
}

// Close stops intake. Run returns after delivering what is buffered.
func (c *Collector) Close() { c.buf.Close() }

// Wait blocks until Run has returned.
func (c *Collector) Wait() { <-c.done }

// Emit stamps and enqueues an event produced by Kestrel itself (lifecycle,
// probes). Message text is redacted like any other.
func (c *Collector) Emit(ev api.Event) {
	// Supervisor messages embed text Kestrel didn't write (command lines,
	// probe errors, a crash's last words), so they get the same treatment as
	// output lines: control characters out, then redaction.
	ev.Message = c.opts.Redactor.Redact(clean([]byte(ev.Message)))
	if ev.Time.IsZero() {
		ev.Time = c.stamper.Now()
	}
	_ = c.buf.Push(ev) // only fails after Close, when nothing is listening
}

// Stats exposes buffer occupancy and total drops (for diagnostics and tests).
func (c *Collector) Stats() (events, bytes, droppedTotal int) { return c.buf.Stats() }

// ReadStream reads one output stream until EOF, turning each line into a
// redacted, classified event. Runs of identical lines (by fingerprint) are
// folded: the first is emitted, the rest are summarized in one event.
func (c *Collector) ReadStream(service string, pid int, src api.EventSource, r io.Reader) error {
	red := c.opts.Redactor.NewStream()
	d := &deduper{c: c, window: c.opts.RepeatWindow}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(c.opts.RepeatIdle / 2)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				d.flushIdle(c.opts.RepeatIdle)
			}
		}
	}()

	err := ReadLines(r, c.opts.MaxLine, func(line string, truncated int) {
		msg := red.Line(line)
		if truncated > 0 {
			msg += TruncationMarker(truncated)
		}
		cl := events.Classify(msg)
		ev := api.Event{
			Time:      c.stamper.Now(),
			ServiceID: service,
			PID:       pid,
			Type:      cl.Type,
			Severity:  cl.Severity,
			Message:   msg,
			Source:    src,
			Attrs: api.Attrs{
				PatternID: cl.PatternID,
				Port:      cl.Port,
				Target:    cl.Target,
				Truncated: truncated > 0,
			},
		}
		d.add(ev)
	})
	close(stop)
	wg.Wait()
	d.flush()
	return err
}

// deduper folds consecutive repeats of the same fingerprint on one stream.
type deduper struct {
	c      *Collector
	window time.Duration

	mu      sync.Mutex
	first   *api.Event // the emitted first occurrence of the current run
	repeats int
	last    time.Time
}

func (d *deduper) add(ev api.Event) {
	ev.Fingerprint = events.Fingerprint(ev.Message)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.first != nil && d.first.Fingerprint == ev.Fingerprint && ev.Time.Sub(d.first.Time) < d.window {
		d.repeats++
		d.last = ev.Time
		return
	}
	d.flushLocked()
	d.first, d.last = &ev, ev.Time
	_ = d.c.buf.Push(ev)
}

func (d *deduper) flushIdle(idle time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.repeats > 0 && d.c.stamper.Now().Sub(d.last) >= idle {
		d.flushLocked()
	}
}

func (d *deduper) flush() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.flushLocked()
}

// flushLocked emits the repeat summary for the current run, if any, and
// starts a new run.
func (d *deduper) flushLocked() {
	if d.first == nil {
		return
	}
	if d.repeats > 0 {
		sum := *d.first
		sum.Time = d.last
		sum.Attrs.Repeats = d.repeats
		sum.Attrs.DurationMS = d.last.Sub(d.first.Time).Milliseconds()
		_ = d.c.buf.Push(sum)
	}
	d.first, d.repeats = nil, 0
}
