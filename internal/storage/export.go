package storage

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

const truncMarker = "bytes in segment]"

// RedactFunc scrubs a string. Export applies it to every text field on top of
// the redaction that already happened before storage.
type RedactFunc func(string) string

// Export streams a session as one JSON document: summary, final service
// states, every event (full messages restored from segments), and a trailing
// list of warnings about unreadable data. The document is always valid JSON.
func (s *Store) Export(ctx context.Context, sessionID string, redact RedactFunc, w io.Writer) error {
	if redact == nil {
		redact = func(x string) string { return x }
	}
	sum, services, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	var warnings []string
	full, warnings := s.fullMessages(ctx, sessionID, warnings)

	bw := bufio.NewWriter(w)
	head := func(key string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(bw, "  %q: %s,\n", key, b)
		return err
	}
	redactSummary := func(sm api.SessionSummary) api.SessionSummary {
		sm.ProjectRoot = redact(sm.ProjectRoot)
		return sm
	}
	for i := range services {
		services[i].Probe, services[i].Detail = redact(services[i].Probe), redact(services[i].Detail)
	}
	if _, err := bw.WriteString("{\n"); err != nil {
		return err
	}
	for _, kv := range []struct {
		k string
		v any
	}{
		{"schema_version", api.SchemaVersion},
		{"exported_at", s.opts.Now().UTC().Format(time.RFC3339)},
		{"redaction", "best-effort pattern redaction applied before storage and again on export; review before sharing"},
		{"session", redactSummary(sum)},
		{"services", services},
	} {
		if err := head(kv.k, kv.v); err != nil {
			return err
		}
	}
	if _, err := bw.WriteString(`  "events": [`); err != nil {
		return err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT seq, id, time_ns, service, pid, type, severity, source, message, attrs, fingerprint
		FROM events WHERE session_id = ? ORDER BY seq`, sessionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	n := 0
	truncated := 0
	for rows.Next() {
		var (
			e                    api.Event
			t                    int64
			attrs, typ, sev, src string
		)
		if err := rows.Scan(&e.Seq, &e.ID, &t, &e.ServiceID, &e.PID, &typ, &sev, &src, &e.Message, &attrs, &e.Fingerprint); err != nil {
			return err
		}
		e.SessionID, e.Time = sessionID, time.Unix(0, t).UTC()
		e.Type, e.Severity, e.Source = api.EventType(typ), api.Severity(sev), api.EventSource(src)
		_ = json.Unmarshal([]byte(attrs), &e.Attrs)
		if strings.HasSuffix(e.Message, truncMarker) {
			if m, ok := full[e.Seq]; ok {
				e.Message = m
			} else {
				truncated++
			}
		}
		e.Message = redact(e.Message)
		e.Attrs.Target = redact(e.Attrs.Target)
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		sep := "\n    "
		if n > 0 {
			sep = ",\n    "
		}
		if _, err := bw.WriteString(sep); err != nil {
			return err
		}
		if _, err := bw.Write(b); err != nil {
			return err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if truncated > 0 {
		warnings = append(warnings, fmt.Sprintf("%d long message(s) are truncated because their segment could not be read", truncated))
	}
	if sum.WriteErrors > 0 {
		warnings = append(warnings, fmt.Sprintf("%d event(s) could not be fully stored during the session; this record is incomplete", sum.WriteErrors))
	}
	if n > 0 {
		bw.WriteString("\n  ")
	}
	wb, _ := json.Marshal(warnings)
	if warnings == nil {
		wb = []byte("[]")
	}
	if _, err := fmt.Fprintf(bw, "],\n  \"warnings\": %s\n}\n", wb); err != nil {
		return err
	}
	return bw.Flush()
}

// fullMessages returns the full text of every event whose SQLite message was
// truncated, read from segment files. Unreadable segments become warnings.
func (s *Store) fullMessages(ctx context.Context, sessionID string, warnings []string) (map[uint64]string, []string) {
	need := map[uint64]bool{}
	segs := map[int64]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT seq, segment_id FROM events
		WHERE session_id = ? AND segment_id IS NOT NULL AND instr(message, ?) > 0`, sessionID, truncMarker)
	if err != nil {
		return nil, append(warnings, "could not look up truncated messages: "+err.Error())
	}
	for rows.Next() {
		var seq uint64
		var seg int64
		if rows.Scan(&seq, &seg) == nil {
			need[seq], segs[seg] = true, true
		}
	}
	rows.Close()
	out := map[uint64]string{}
	if len(need) == 0 {
		return out, warnings
	}
	for id := range segs {
		var rel string
		if err := s.db.QueryRowContext(ctx, `SELECT path FROM segments WHERE id = ?`, id).Scan(&rel); err != nil {
			continue
		}
		if err := s.scanSegment(rel, func(e api.Event) {
			if need[e.Seq] {
				out[e.Seq] = e.Message
			}
		}); err != nil {
			warnings = append(warnings, fmt.Sprintf("segment %s: %v", rel, err))
		}
	}
	return out, warnings
}

// scanSegment calls fn for every complete event line in a segment. A final
// line cut short by a crash is ignored.
func (s *Store) scanSegment(rel string, fn func(api.Event)) error {
	path, err := s.segmentPath(rel)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errors.New("file is missing")
		}
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(rel, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("not valid gzip: %w", err)
		}
		defer zr.Close()
		r = zr
	}
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			var e api.Event
			if json.Unmarshal(line, &e) == nil {
				fn(e)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
