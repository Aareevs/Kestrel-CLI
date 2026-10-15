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
//     make room, and the drop is counted.
//   - A droppable event arriving when only protected events fill the buffer is
//     itself discarded and counted.
//   - A protected event arriving when only protected events fill the buffer
//     blocks the producer until there is room. Blocking the reader stops
//     draining the child's pipe, which makes the child's own writes block:
//     backpressure reaches the source instead of data being lost.
//
// Events leave in Seq order. Pop reports how many events were dropped since
// the previous report so the consumer can emit a collector_overload event.
type Buffer struct {
	mu        sync.Mutex
	cond      *sync.Cond
	drop      queue // droppable events
	keep      queue // protected events
	bytes     int
	maxEvents int
	maxBytes  int
	dropped   int
	total     int
	closed    bool
}

// NewBuffer returns a buffer bounded by both event count and approximate bytes.
func NewBuffer(maxEvents, maxBytes int) *Buffer {
	b := &Buffer{maxEvents: maxEvents, maxBytes: maxBytes}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *Buffer) full(extra int) bool {
	n := b.drop.len() + b.keep.len()
	return n > 0 && (n >= b.maxEvents || b.bytes+extra > b.maxBytes)
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
			b.dropped++
			b.total++
			continue
		}
		if Droppable(&ev) {
			b.dropped++
			b.total++
			return nil
		}
		b.cond.Wait()
	}
	if Droppable(&ev) {
		b.drop.push(ev)
	} else {
		b.keep.push(ev)
	}
	b.bytes += cost
	b.cond.Broadcast()
	return nil
}

// Pop blocks until an event is available and returns it with the number of
// events dropped since the last Pop that reported drops. ok is false once the
// buffer is closed and empty.
func (b *Buffer) Pop() (ev api.Event, dropped int, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.drop.len()+b.keep.len() == 0 {
		if b.closed {
			dropped, b.dropped = b.dropped, 0
			return api.Event{}, dropped, false
		}
		b.cond.Wait()
	}
	var q *queue
	switch {
	case b.drop.len() == 0:
		q = &b.keep
	case b.keep.len() == 0:
		q = &b.drop
	case b.drop.peek().Seq < b.keep.peek().Seq:
		q = &b.drop
	default:
		q = &b.keep
	}
	ev = q.pop()
	b.bytes -= eventCost(&ev)
	dropped, b.dropped = b.dropped, 0
	b.cond.Broadcast()
	return ev, dropped, true
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
