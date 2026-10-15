package storage

import (
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

const (
	batchMax       = 256
	batchInterval  = 100 * time.Millisecond
	maxRowMessage  = 1024 // bytes of message kept in SQLite; the segment has all of it
	rowOverhead    = 200  // rough size of an event row beyond its message
	recorderBuffer = 1024
)

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func fileSafe(service string) string {
	if service == "" {
		return "_session"
	}
	return unsafeName.ReplaceAllString(service, "_")
}

// Recorder persists one session's events. Emit is called from the collector's
// single delivery goroutine; a slow disk therefore makes Emit block, which
// fills the collector buffer, which drops debug/info lines (and reports it).
// Errors, lifecycle events, and everything else protected are never dropped
// by the recorder: if storage fails, the failure is counted on the session
// (write_errors) and supervision carries on.
type Recorder struct {
	store     *Store
	sessionID string

	mu     sync.RWMutex // guards closed against concurrent Emit/Close
	closed bool
	ch     chan api.Event
	done   chan struct{}

	writers map[string]*segWriter // owned by the run goroutine
	counter map[string]int

	writeErrors atomic.Int64
	errMu       sync.Mutex
	firstErr    error
}

type segWriter struct {
	id      int64
	service string
	rel     string // relative to the sessions directory
	f       *os.File
	bw      *bufio.Writer
	size    int64
	first   uint64
	last    uint64
	failed  bool
	touched bool
}

// NewRecorder starts persisting events for a session that CreateSession has
// already registered.
func (s *Store) NewRecorder(sessionID string) *Recorder {
	r := &Recorder{
		store: s, sessionID: sessionID,
		ch:      make(chan api.Event, recorderBuffer),
		done:    make(chan struct{}),
		writers: map[string]*segWriter{},
		counter: map[string]int{},
	}
	go r.run()
	return r
}

// Emit queues an event for persistence.
func (r *Recorder) Emit(ev api.Event) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		r.fail(errors.New("event arrived after the recorder was closed"), 1)
		return
	}
	r.ch <- ev
}

// WriteErrors reports how many events could not be fully persisted so far.
func (r *Recorder) WriteErrors() int { return int(r.writeErrors.Load()) }

// Err returns the first storage error seen, if any.
func (r *Recorder) Err() error {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	return r.firstErr
}

func (r *Recorder) fail(err error, events int) {
	r.writeErrors.Add(int64(events))
	r.errMu.Lock()
	if r.firstErr == nil {
		r.firstErr = err
		slog.Warn("session storage problem; the stored record will be incomplete", "session", r.sessionID, "err", err)
	}
	r.errMu.Unlock()
}

// Close flushes everything, finalizes (compresses) open segments, and
// writes the session's final counters. It returns the first storage error.
func (r *Recorder) Close() error {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.ch)
	}
	r.mu.Unlock()
	<-r.done
	return r.Err()
}

func (r *Recorder) run() {
	defer close(r.done)
	batch := make([]api.Event, 0, batchMax)
	t := time.NewTicker(batchInterval)
	defer t.Stop()
	for {
		select {
		case ev, ok := <-r.ch:
			if !ok {
				r.flush(batch)
				r.finalize()
				return
			}
			batch = append(batch, ev)
			if len(batch) >= batchMax {
				r.flush(batch)
				batch = batch[:0]
			}
		case <-t.C:
			if len(batch) > 0 {
				r.flush(batch)
				batch = batch[:0]
			}
		}
	}
}

func truncateMessage(m string) string {
	if len(m) <= maxRowMessage {
		return m
	}
	cut := maxRowMessage
	for cut > 0 && !utf8.RuneStart(m[cut]) {
		cut--
	}
	return m[:cut] + " …[+" + strconv.Itoa(len(m)-cut) + " bytes in segment]"
}

// flush writes a batch: segment lines first, then all rows and counters in
// one transaction.
func (r *Recorder) flush(batch []api.Event) {
	if len(batch) == 0 {
		return
	}
	ctx := context.Background()
	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		r.fail(fmt.Errorf("begin transaction: %w", err), len(batch))
		return
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO events
		(session_id, seq, id, time_ns, service, pid, type, severity, source, message, attrs, fingerprint, segment_id, seg_offset)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		r.fail(fmt.Errorf("prepare insert: %w", err), len(batch))
		return
	}
	defer stmt.Close()

	var events, errs, dropped, rowBytes, segBytes int
	failedInBatch := 0
	for i := range batch {
		ev := &batch[i]
		segID, off, werr := r.writeSegment(ctx, tx, ev)
		if werr != nil {
			r.fail(werr, 1)
			failedInBatch++
		} else if segID != 0 {
			segBytes += int(r.writers[ev.ServiceID].size - off)
		}
		attrs, _ := json.Marshal(ev.Attrs)
		msg := truncateMessage(ev.Message)
		var seg, offset any
		if segID != 0 && werr == nil {
			seg, offset = segID, off
		}
		if _, err := stmt.ExecContext(ctx, r.sessionID, ev.Seq, ev.ID, ev.Time.UnixNano(), ev.ServiceID, ev.PID,
			string(ev.Type), string(ev.Severity), string(ev.Source), msg, string(attrs), ev.Fingerprint, seg, offset); err != nil {
			r.fail(fmt.Errorf("insert event seq %d: %w", ev.Seq, err), 1)
			failedInBatch++
			continue
		}
		events++
		rowBytes += len(msg) + len(attrs) + rowOverhead
		if ev.Severity.Rank() >= api.SeverityError.Rank() {
			errs++
		}
		if ev.Type == api.EventCollectorOverload {
			dropped += ev.Attrs.Dropped
		}
	}
	// Push buffered segment bytes to the OS and update the segment index.
	for _, w := range r.writers {
		if !w.touched {
			continue
		}
		if w.bw != nil {
			if err := w.bw.Flush(); err != nil {
				r.fail(fmt.Errorf("flush segment %s: %w", w.rel, err), 1)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE segments SET last_seq = ?, bytes = ?, disk_bytes = ? WHERE id = ?`,
			w.last, w.size, w.size, w.id); err != nil {
			r.fail(fmt.Errorf("update segment index: %w", err), 0)
		}
		w.touched = false
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET events = events + ?, errors = errors + ?, dropped = dropped + ?,
		bytes = bytes + ?, write_errors = write_errors + ? WHERE id = ?`,
		events, errs, dropped, rowBytes+segBytes, failedInBatch, r.sessionID); err != nil {
		r.fail(fmt.Errorf("update session counters: %w", err), 0)
	}
	if err := tx.Commit(); err != nil {
		r.fail(fmt.Errorf("commit: %w", err), len(batch))
		return
	}
	for key, w := range r.writers {
		if w.size >= r.store.opts.SegmentMax {
			r.closeSegment(ctx, w)
			delete(r.writers, key)
		}
	}
}

// writeSegment appends the event to its service's segment, creating the
// segment (and its index row, inside tx) when needed. It returns the segment
// ID and the byte offset of the line.
func (r *Recorder) writeSegment(ctx context.Context, tx *sql.Tx, ev *api.Event) (int64, int64, error) {
	// Writers are keyed by the real service name; file names are sanitized
	// and numbered, so two names that sanitize alike still get distinct files.
	key := ev.ServiceID
	w := r.writers[key]
	if w == nil {
		name := fileSafe(ev.ServiceID)
		r.counter[name]++
		rel := filepath.Join(r.sessionID, fmt.Sprintf("%s-%03d.log", name, r.counter[name]))
		full := filepath.Join(sessionsDir(r.store.dir), rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return 0, 0, fmt.Errorf("create segment directory: %w", err)
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO segments (session_id, service, path, first_seq, last_seq) VALUES (?, ?, ?, ?, ?)`,
			r.sessionID, ev.ServiceID, filepath.ToSlash(rel), ev.Seq, ev.Seq)
		if err != nil {
			return 0, 0, fmt.Errorf("index segment: %w", err)
		}
		id, _ := res.LastInsertId()
		w = &segWriter{id: id, service: ev.ServiceID, rel: filepath.ToSlash(rel), first: ev.Seq}
		f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			w.failed = true
			r.writers[key] = w
			return 0, 0, fmt.Errorf("create segment: %w", err)
		}
		w.f, w.bw = f, bufio.NewWriterSize(f, 64<<10)
		r.writers[key] = w
	}
	if w.failed {
		return 0, 0, fmt.Errorf("segment %s is unavailable", w.rel)
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return 0, 0, err
	}
	line = append(line, '\n')
	off := w.size
	if _, err := w.bw.Write(line); err != nil {
		w.failed = true
		return 0, 0, fmt.Errorf("write segment %s: %w", w.rel, err)
	}
	w.size += int64(len(line))
	w.last, w.touched = ev.Seq, true
	return w.id, off, nil
}

// closeSegment finishes a segment: close the file, then seal it (gzip +
// index update). Failures leave the plain .log in place, which stays readable.
func (r *Recorder) closeSegment(ctx context.Context, w *segWriter) {
	if w.bw != nil {
		if err := w.bw.Flush(); err != nil {
			r.fail(fmt.Errorf("flush segment %s: %w", w.rel, err), 0)
		}
	}
	if w.f != nil {
		w.f.Close()
	}
	if w.failed || w.f == nil {
		return
	}
	if err := r.store.sealSegment(ctx, r.sessionID, w.id, w.rel, w.size); err != nil {
		r.fail(err, 0)
	}
}

// sealSegment gzips a closed segment and atomically points the index at the
// compressed file. size is the uncompressed size, used to adjust the
// session's on-disk total.
func (s *Store) sealSegment(ctx context.Context, sessionID string, id int64, rel string, size int64) error {
	full := filepath.Join(sessionsDir(s.dir), filepath.FromSlash(rel))
	gz, err := compressFile(full)
	if err != nil {
		_, _ = s.db.ExecContext(ctx, `UPDATE segments SET closed = 1 WHERE id = ?`, id)
		return fmt.Errorf("compress segment %s: %w", rel, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE segments SET path = ?, disk_bytes = ?, compressed = 1, closed = 1 WHERE id = ?`,
		rel+".gz", gz, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET bytes = bytes - ? WHERE id = ?`, size-gz, sessionID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Only now is the plain file redundant.
	_ = os.Remove(full)
	return nil
}

// finalize closes every open segment when the session ends.
func (r *Recorder) finalize() {
	ctx := context.Background()
	for key, w := range r.writers {
		r.closeSegment(ctx, w)
		delete(r.writers, key)
	}
	if n := r.writeErrors.Load(); n > 0 {
		// Counted per batch inside transactions; make sure the total survives
		// even if a transaction rolled back.
		_, _ = r.store.db.ExecContext(ctx, `UPDATE sessions SET write_errors = MAX(write_errors, ?) WHERE id = ?`, n, r.sessionID)
	}
}

// compressFile gzips path to path+".gz" (via a temp file and rename) and
// returns the compressed size. The original is left for the caller to remove
// once the index points at the new file.
func compressFile(path string) (int64, error) {
	in, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".compress-*")
	if err != nil {
		return 0, err
	}
	zw := gzip.NewWriter(tmp)
	if _, err := io.Copy(zw, in); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return 0, err
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return 0, err
	}
	fi, err := tmp.Stat()
	if err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return 0, err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		os.Remove(tmp.Name())
		return 0, err
	}
	if err := os.Rename(tmp.Name(), path+".gz"); err != nil {
		os.Remove(tmp.Name())
		return 0, err
	}
	return fi.Size(), nil
}
