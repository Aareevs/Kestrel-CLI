// Package discovery scans a project directory and proposes service candidates.
//
// Discovery only reads files. It never executes project scripts, never opens
// .env files, never follows symlinks out of the project root, and stops at
// fixed depth, file-count, file-size, and time limits.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kestrel-cli/kestrel/pkg/api"
)

// Options bound a scan. Zero values take defaults.
type Options struct {
	Root        string
	MaxDepth    int           // directory depth below Root (default 3)
	MaxFiles    int           // files enumerated before stopping (default 5000)
	MaxFileSize int64         // largest file that will be read (default 1 MiB)
	Budget      time.Duration // wall-clock budget (default 3s)
	Now         func() time.Time
}

const (
	DefaultMaxDepth    = 3
	DefaultMaxFiles    = 5000
	DefaultMaxFileSize = 1 << 20
	DefaultBudget      = 3 * time.Second
)

func (o *Options) defaults() {
	if o.MaxDepth <= 0 {
		o.MaxDepth = DefaultMaxDepth
	}
	if o.MaxFiles <= 0 {
		o.MaxFiles = DefaultMaxFiles
	}
	if o.MaxFileSize <= 0 {
		o.MaxFileSize = DefaultMaxFileSize
	}
	if o.Budget <= 0 {
		o.Budget = DefaultBudget
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// skipDirs are never descended into.
var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
	"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true,
	".venv": true, "venv": true, "__pycache__": true, ".tox": true, ".mypy_cache": true, ".pytest_cache": true,
	".next": true, ".nuxt": true, ".svelte-kit": true, ".turbo": true, ".cache": true, ".parcel-cache": true,
	".gradle": true, ".idea": true, ".terraform": true, "coverage": true, "out": true, "tmp": true,
}

var errTooLarge = errors.New("file too large")

// scanner holds the state of one scan.
type scanner struct {
	ctx      context.Context
	opts     Options
	root     string              // absolute, symlinks resolved
	files    map[string][]string // dir (slash-rel, "." for root) → file names
	ignores  map[string]*gitignore
	stats    api.ScanStats
	warnings []string
	deadline time.Time
	// extraTech records technologies seen without a runnable candidate
	// (e.g. a library-only go.mod).
	extraTech []string
}

// Scan inspects the project at opts.Root.
func Scan(ctx context.Context, opts Options) (*api.InspectResult, error) {
	opts.defaults()
	abs, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(root); err != nil {
		return nil, err
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	s := &scanner{
		ctx:      ctx,
		opts:     opts,
		root:     root,
		files:    map[string][]string{},
		ignores:  map[string]*gitignore{},
		deadline: opts.Now().Add(opts.Budget),
		stats:    api.ScanStats{MaxDepth: opts.MaxDepth, MaxFiles: opts.MaxFiles},
	}
	if err := s.walk(); err != nil {
		return nil, err
	}

	cands := s.analyze()
	return &api.InspectResult{
		SchemaVersion: api.SchemaVersion,
		Root:          root,
		Technologies:  s.technologies(cands),
		Candidates:    cands,
		Scan:          s.stats,
		Warnings:      s.warnings,
	}, nil
}

func (s *scanner) warnf(format string, args ...any) {
	s.warnings = append(s.warnings, fmt.Sprintf(format, args...))
}

func (s *scanner) truncate(reason string) {
	if !s.stats.Truncated {
		s.stats.Truncated = true
		s.stats.TruncatedReason = reason
	}
}

func (s *scanner) walk() error {
	err := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
		if s.ctx.Err() != nil {
			s.truncate("cancelled")
			return filepath.SkipAll
		}
		if s.opts.Now().After(s.deadline) {
			s.truncate(fmt.Sprintf("time budget of %s exceeded", s.opts.Budget))
			return filepath.SkipAll
		}
		rel, rerr := filepath.Rel(s.root, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if err != nil {
			if rel == "." {
				return err
			}
			s.warnf("could not read %s: %v", rel, unwrapPathErr(err))
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			if rel != "." {
				if skipDirs[d.Name()] || s.ignored(rel, true) {
					return filepath.SkipDir
				}
				if strings.Count(rel, "/")+1 > s.opts.MaxDepth {
					return filepath.SkipDir
				}
			}
			s.stats.DirsSeen++
			s.loadGitignore(rel)
			return nil
		}

		if s.ignored(rel, false) {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			// Directory symlinks are never followed; file symlinks are only
			// read if they resolve inside the root (checked in read).
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				return nil
			}
		} else if !d.Type().IsRegular() {
			return nil
		}
		if s.stats.FilesSeen >= s.opts.MaxFiles {
			s.truncate(fmt.Sprintf("file limit of %d reached", s.opts.MaxFiles))
			return filepath.SkipAll
		}
		s.stats.FilesSeen++
		dir := path.Dir(rel)
		s.files[dir] = append(s.files[dir], d.Name())
		return nil
	})
	return err
}

func unwrapPathErr(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// has reports whether a file was seen during the walk.
func (s *scanner) has(rel string) bool {
	dir, name := path.Split(rel)
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		dir = "."
	}
	for _, n := range s.files[dir] {
		if n == name {
			return true
		}
	}
	return false
}

// read returns the contents of a project file, refusing files that are too
// large, not regular, or that resolve (via symlinks) outside the root.
func (s *scanner) read(rel string) ([]byte, error) {
	p := filepath.Join(s.root, filepath.FromSlash(rel))
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return nil, err
	}
	if resolved != s.root && !strings.HasPrefix(resolved, s.root+string(filepath.Separator)) {
		s.warnf("skipped %s: symlink points outside the project root", rel)
		return nil, fs.ErrPermission
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fs.ErrInvalid
	}
	if fi.Size() > s.opts.MaxFileSize {
		s.warnf("skipped %s: larger than %d bytes", rel, s.opts.MaxFileSize)
		return nil, errTooLarge
	}
	f, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, s.opts.MaxFileSize))
}

// readSeen reads a file only if the walk saw it.
func (s *scanner) readSeen(rel string) ([]byte, bool) {
	if !s.has(rel) {
		return nil, false
	}
	b, err := s.read(rel)
	if err != nil {
		return nil, false
	}
	return b, true
}

// readOptional reads a conventional file that may sit below the depth limit
// (e.g. src/main/resources/application.properties). It still obeys every
// other safety rule, including .gitignore and the skip list.
func (s *scanner) readOptional(rel string) ([]byte, bool) {
	for dir := path.Dir(rel); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if skipDirs[path.Base(dir)] || s.ignored(dir, true) {
			return nil, false
		}
	}
	if s.ignored(rel, false) {
		return nil, false
	}
	p := filepath.Join(s.root, filepath.FromSlash(rel))
	if _, err := os.Lstat(p); err != nil {
		return nil, false
	}
	b, err := s.read(rel)
	if err != nil {
		return nil, false
	}
	return b, true
}

func (s *scanner) loadGitignore(dir string) {
	rel := ".gitignore"
	if dir != "." {
		rel = dir + "/.gitignore"
	}
	p := filepath.Join(s.root, filepath.FromSlash(rel))
	if fi, err := os.Lstat(p); err != nil || fi.IsDir() {
		return
	}
	b, err := s.read(rel)
	if err != nil {
		return
	}
	s.ignores[dir] = parseGitignore(string(b))
}

// ignored applies .gitignore files from the root down to rel's parent; the
// last matching rule wins.
func (s *scanner) ignored(rel string, isDir bool) bool {
	if len(s.ignores) == 0 {
		return false
	}
	ignored := false
	parts := strings.Split(rel, "/")
	for i := 0; i < len(parts); i++ {
		base := "."
		if i > 0 {
			base = strings.Join(parts[:i], "/")
		}
		gi := s.ignores[base]
		if gi == nil {
			continue
		}
		sub := strings.Join(parts[i:], "/")
		if m, neg := gi.match(sub, isDir); m {
			ignored = !neg
		}
	}
	return ignored
}

func (s *scanner) technologies(cands []api.ServiceCandidate) []string {
	set := map[string]bool{}
	for _, c := range cands {
		set[c.Technology] = true
	}
	for _, names := range s.files {
		for _, n := range names {
			switch {
			case n == "Dockerfile":
				set["docker"] = true
			case composeFiles[n]:
				set["docker-compose"] = true
			}
		}
	}
	for _, t := range s.extraTech {
		set[t] = true
	}
	out := make([]string, 0, len(set))
	for t := range set {
		if t != "" && t != "container" {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// dirLabel renders a slash-relative dir as shown to users.
func dirLabel(rel string) string {
	if rel == "." {
		return "."
	}
	return "./" + rel
}

// joinRel joins a dir and a name into a slash-relative path.
func joinRel(dir, name string) string {
	if dir == "." {
		return name
	}
	return dir + "/" + name
}

// sortedDirs returns the scanned directories in lexical order.
func (s *scanner) sortedDirs() []string {
	dirs := make([]string, 0, len(s.files))
	for d := range s.files {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs
}
