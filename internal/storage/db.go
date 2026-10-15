// Package storage persists sessions: structured events in SQLite, raw redacted
// event text in append-only segment files (rotated, gzipped when closed), and
// retention that keeps the whole thing within configured limits.
//
// Everything stored has already been redacted by internal/privacy.
package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no cgo)
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migration is one forward-only schema change, applied in a transaction.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// builtinMigrations parses the embedded NNN_name.sql files.
func builtinMigrations() []Migration {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		panic(err)
	}
	var out []Migration
	for _, e := range entries {
		num, name, ok := strings.Cut(strings.TrimSuffix(e.Name(), ".sql"), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil {
			panic("storage: bad migration file name " + e.Name())
		}
		b, err := fs.ReadFile(migrationFS, "migrations/"+e.Name())
		if err != nil {
			panic(err)
		}
		out = append(out, Migration{Version: v, Name: name, SQL: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out
}

// Errors callers can match.
var (
	// ErrCorrupt means the database file exists but cannot be used as a
	// SQLite database. Kestrel never deletes or repairs it automatically.
	ErrCorrupt = errors.New("session database is corrupt")
	// ErrTooNew means the database was written by a newer Kestrel.
	ErrTooNew = errors.New("session database was created by a newer Kestrel")
)

// Options configure Open. Zero values take defaults.
type Options struct {
	// Migrations overrides the embedded set (tests).
	Migrations []Migration
	// SegmentMax is the size at which a segment is rotated (default 4 MiB).
	SegmentMax int64
	Now        func() time.Time
}

// Store is an open session database plus its segment directory.
type Store struct {
	dir  string
	db   *sql.DB
	opts Options
}

// DBPath is the database file inside a data directory.
func DBPath(dir string) string { return filepath.Join(dir, "kestrel.db") }

func sessionsDir(dir string) string { return filepath.Join(dir, "sessions") }

func dsn(path, mode string) string {
	q := "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	if mode == "ro" {
		return "file:" + path + "?mode=ro&" + q
	}
	// auto_vacuum only takes effect on a brand-new database; it lets
	// retention return space to the OS with incremental_vacuum.
	return "file:" + path + "?" + q + "&_pragma=journal_mode(WAL)&_pragma=auto_vacuum(2)"
}

// Open creates the data directory if needed, opens (or creates) the
// database, and applies pending migrations.
func Open(ctx context.Context, dir string, opts Options) (*Store, error) {
	if opts.SegmentMax <= 0 {
		opts.SegmentMax = 4 << 20
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Migrations == nil {
		opts.Migrations = builtinMigrations()
	}
	if err := os.MkdirAll(sessionsDir(dir), 0o700); err != nil {
		return nil, fmt.Errorf("data directory: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(DBPath(dir), "rw"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{dir: dir, db: db, opts: opts}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// Dir returns the data directory.
func (s *Store) Dir() string { return s.dir }

func classify(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "file is not a database") || strings.Contains(msg, "database disk image is malformed") {
		return fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return err
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_ns INTEGER NOT NULL)`); err != nil {
		return classify(err)
	}
	applied := map[int]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return classify(err)
	}
	maxApplied := 0
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
		maxApplied = max(maxApplied, v)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	known := 0
	for _, m := range s.opts.Migrations {
		known = max(known, m.Version)
	}
	if maxApplied > known {
		return fmt.Errorf("%w (schema version %d, this build knows %d); upgrade Kestrel", ErrTooNew, maxApplied, known)
	}
	for _, m := range s.opts.Migrations {
		if applied[m.Version] {
			continue
		}
		if err := s.apply(ctx, m); err != nil {
			return fmt.Errorf("migration %03d_%s failed (database left at version %d): %w", m.Version, m.Name, maxApplied, err)
		}
		maxApplied = m.Version
	}
	return nil
}

// apply runs one migration and records it atomically: either both happen or
// neither does. SQLite supports transactional DDL.
func (s *Store) apply(ctx context.Context, m Migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_ns) VALUES (?, ?, ?)`,
		m.Version, m.Name, s.opts.Now().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

// SchemaVersion returns the highest applied migration.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	return int(v.Int64), err
}

// Health describes the state of an existing database without changing it.
type Health struct {
	Exists        bool
	Path          string
	SchemaVersion int
	Sessions      int
	Active        int
	WriteErrors   int // sessions with incomplete records
	DiskBytes     int64
	Problem       error // ErrCorrupt, ErrTooNew, or an open/read failure
}

// Inspect examines the database in dir read-only. It never creates files,
// runs migrations, or repairs anything, so `kestrel doctor` is safe anywhere.
func Inspect(ctx context.Context, dir string) Health {
	h := Health{Path: DBPath(dir)}
	fi, err := os.Stat(h.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return h
	}
	if err != nil {
		h.Problem = err
		return h
	}
	h.Exists, h.DiskBytes = true, fi.Size()
	db, err := sql.Open("sqlite", dsn(h.Path, "ro"))
	if err != nil {
		h.Problem = err
		return h
	}
	defer db.Close()
	var res string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&res); err != nil {
		h.Problem = classify(err)
		return h
	}
	if res != "ok" {
		h.Problem = fmt.Errorf("%w: quick_check: %s", ErrCorrupt, res)
		return h
	}
	var v sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		h.Problem = classify(err)
		return h
	}
	h.SchemaVersion = int(v.Int64)
	known := 0
	for _, m := range builtinMigrations() {
		known = max(known, m.Version)
	}
	if h.SchemaVersion > known {
		h.Problem = fmt.Errorf("%w (schema version %d, this build knows %d)", ErrTooNew, h.SchemaVersion, known)
		return h
	}
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(status = 'active'), 0), COALESCE(SUM(write_errors > 0), 0) FROM sessions`).
		Scan(&h.Sessions, &h.Active, &h.WriteErrors)
	return h
}
