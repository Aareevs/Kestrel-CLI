package collectors

import (
	"errors"
	"sync"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// ErrClosed is returned by Push after Close.
var ErrClosed = errors.New("collector buffer closed")

// Droppable reports whether ev may be discarded under backpressure: only
// debug/info log lines. Errors, fatals, and lifecycle events are protected.
func Droppable(ev *api.Event) bool {
	return ev.Type == api.EventLogLine && ev.Severity.Rank() <= api.SeverityInfo.Rank()
}

// eventCost approximates an event's memory footprint for the byte bound.
func eventCost(ev *api.Event) int { return len(ev.Message) + len(ev.ServiceID) + 256 }

// queue is a FIFO slice queue.
type queue struct {
	items []api.Event
	head  int
}

func (q *queue) len() int { return len(q.items) - q.head }

func (q *queue) push(ev api.Event) { q.items = append(q.items, ev) }

func (q *queue) peek() *api.Event { return &q.items[q.head] }

func (q *queue) pop() api.Event {
	ev := q.items[q.head]
	q.items[q.head] = api.Event{} // release the message for GC
	q.head++
	if q.head > 1024 && q.head*2 > len(q.items) {
		q.items = append(q.items[:0:0], q.items[q.head:]...)
		q.head = 0
	}
	return ev
}

// Buffer is a bounded event queue with an explicit backpressure policy:
//
//   - When full, the oldest droppable (debug/info log) event is discarded to
//     make room.
//   - A droppable event arriving when only protected events fill the buffer is
//     itself discarded.
//   - A protected event arriving when only protected events fill the buffer
//     blocks the producer until there is room. Blocking the reader stops
//     draining the child's pipe, which makes the child's own writes block:
//     backpressure reaches the source instead of data being lost.
//
// Every drop is counted on a collector_overload marker that takes its place
// in the ordered stream at the first drop and accumulates later drops until
// it is delivered.
//
// Sequence numbers are assigned here, under the lock, at the moment an event
// is inserted, so delivery order always equals Seq order even with many
// concurrent producers.
type Buffer struct {
	mu        sync.Mutex
	cond      *sync.Cond
	drop      queue // droppable events
	keep      queue // protected events
	marker    *api.Event
	bytes     int
	maxEvents int
	maxBytes  int
	total     int
	closed    bool

	stamp    func(*api.Event)
	overload func() api.Event
}

// NewBuffer returns a buffer bounded by both event count and approximate
// bytes. stamp assigns identity and Seq at insertion; overload builds an
// unstamped collector_overload event. Either may be nil in tests (events
// then keep the Seq they arrive with).
func NewBuffer(maxEvents, maxBytes int, stamp func(*api.Event), overload func() api.Event) *Buffer {
	if stamp == nil {
		stamp = func(*api.Event) {}
	}
	if overload == nil {
		overload = func() api.Event {
			return api.Event{Type: api.EventCollectorOverload, Severity: api.SeverityWarn, Source: api.SourceKestrel}
		}
	}
	b := &Buffer{maxEvents: maxEvents, maxBytes: maxBytes, stamp: stamp, overload: overload}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *Buffer) full(extra int) bool {
	n := b.drop.len() + b.keep.len()
	return n > 0 && (n >= b.maxEvents || b.bytes+extra > b.maxBytes)
}

// countDrop records one discarded event on the pending marker.
func (b *Buffer) countDrop() {
	b.total++
	if b.marker == nil {
		m := b.overload()
		b.stamp(&m)
		b.marker = &m
	}
	b.marker.Attrs.Dropped++
}

// Push adds ev, applying the drop policy. It blocks only for protected events
// when the buffer holds nothing droppable.
func (b *Buffer) Push(ev api.Event) error {
	cost := eventCost(&ev)
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		if b.closed {
			return ErrClosed
		}
		if !b.full(cost) {
			break
		}
		if b.drop.len() > 0 {
			old := b.drop.pop()
			b.bytes -= eventCost(&old)
			b.countDrop()
			continue
		}
		if Droppable(&ev) {
			b.countDrop()
			b.cond.Broadcast()
			return nil
		}
		b.cond.Wait()
	}
	b.stamp(&ev)
	if Droppable(&ev) {
		b.drop.push(ev)
	} else {
		b.keep.push(ev)
	}
	b.bytes += cost
	b.cond.Broadcast()
	return nil
}

// Pop blocks until an event is available and returns the one with the lowest
// Seq. ok is false once the buffer is closed and empty.
func (b *Buffer) Pop() (ev api.Event, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.drop.len()+b.keep.len() == 0 && b.marker == nil {
		if b.closed {
			return api.Event{}, false
		}
		b.cond.Wait()
	}
	var best *api.Event
	var from *queue
	consider := func(e *api.Event, q *queue) {
		if best == nil || e.Seq < best.Seq {
			best, from = e, q
		}
	}
	if b.drop.len() > 0 {
		consider(b.drop.peek(), &b.drop)
	}
	if b.keep.len() > 0 {
		consider(b.keep.peek(), &b.keep)
	}
	if b.marker != nil {
		consider(b.marker, nil)
	}
	if from == nil {
		ev, b.marker = *b.marker, nil
	} else {
		ev = from.pop()
		b.bytes -= eventCost(&ev)
	}
	b.cond.Broadcast()
	return ev, true
}

// Close stops accepting events; Pop drains what remains.
func (b *Buffer) Close() {
	b.mu.Lock()
	b.closed = true
	b.cond.Broadcast()
	b.mu.Unlock()
}

// Stats returns the current length, approximate bytes, and total drops.
func (b *Buffer) Stats() (events, bytes, droppedTotal int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.drop.len() + b.keep.len(), b.bytes, b.total
}
