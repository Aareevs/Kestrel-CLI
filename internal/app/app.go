// Package app holds Kestrel's use cases. The CLI and (later) the MCP server
// both call into this package; neither contains business logic.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/kestrel-cli/kestrel/internal/config"
	"github.com/kestrel-cli/kestrel/internal/discovery"
	"github.com/kestrel-cli/kestrel/pkg/api"
)

// ErrNoConfig is returned when a command needs kestrel.yaml and there is none.
var ErrNoConfig = errors.New("no kestrel.yaml found")

// App is bound to one project root.
type App struct {
	// ProjectRoot is absolute.
	ProjectRoot string
	Getenv      func(string) string
	Now         func() time.Time
}

// New resolves the project root: the explicit --project directory if given,
// else the nearest ancestor of cwd containing a config file, else cwd.
func New(projectFlag, cwd string) (*App, error) {
	root := cwd
	if projectFlag != "" {
		abs, err := filepath.Abs(projectFlag)
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("project directory: %w", err)
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("project directory: %s is not a directory", abs)
		}
		root = abs
	} else {
		root, _ = config.FindProjectRoot(cwd)
	}
	return &App{ProjectRoot: root, Getenv: os.Getenv, Now: time.Now}, nil
}

// ConfigPath returns the project's config file, or "" if there is none.
func (a *App) ConfigPath() (string, error) {
	return config.Find(a.ProjectRoot)
}

// LoadConfig loads and validates the project's config. It returns
// ErrNoConfig when the project has no config file.
func (a *App) LoadConfig() (*config.Config, string, error) {
	p, err := a.ConfigPath()
	if err != nil {
		return nil, "", err
	}
	if p == "" {
		return nil, "", ErrNoConfig
	}
	cfg, err := config.Load(p, a.ProjectRoot)
	return cfg, p, err
}

// ValidateConfig reports every problem in the project's config file.
func (a *App) ValidateConfig() (*api.ConfigValidation, *config.Config, error) {
	cfg, p, err := a.LoadConfig()
	if errors.Is(err, ErrNoConfig) || (err != nil && p == "") {
		return nil, nil, err
	}
	res := &api.ConfigValidation{SchemaVersion: api.SchemaVersion, Path: a.rel(p), Valid: err == nil}
	var ve *config.ValidationError
	switch {
	case err == nil:
	case errors.As(err, &ve):
		for _, is := range ve.Issues {
			res.Issues = append(res.Issues, api.ConfigIssue{Line: is.Line, Field: is.Field, Message: is.Message})
		}
	default:
		return nil, nil, err
	}
	return res, cfg, nil
}

// InspectOptions bound discovery.
type InspectOptions struct {
	MaxDepth int
	MaxFiles int
	Budget   time.Duration
}

// Inspect scans the project and returns service candidates. It reads files
// only and never executes anything.
func (a *App) Inspect(ctx context.Context, o InspectOptions) (*api.InspectResult, error) {
	res, err := discovery.Scan(ctx, discovery.Options{
		Root:     a.ProjectRoot,
		MaxDepth: o.MaxDepth,
		MaxFiles: o.MaxFiles,
		Budget:   o.Budget,
		Now:      a.Now,
	})
	if err != nil {
		return nil, err
	}
	if p, err := a.ConfigPath(); err == nil && p != "" {
		res.ConfigFile = a.rel(p)
	}
	slog.Debug("discovery finished", "root", res.Root, "files", res.Scan.FilesSeen, "dirs", res.Scan.DirsSeen,
		"candidates", len(res.Candidates), "truncated", res.Scan.Truncated, "warnings", len(res.Warnings))
	return res, nil
}

// rel renders a path relative to the project root when possible.
func (a *App) rel(p string) string {
	if r, err := filepath.Rel(a.ProjectRoot, p); err == nil && !filepath.IsAbs(r) && r != ".." && (len(r) < 3 || r[:3] != ".."+string(filepath.Separator)) {
		return r
	}
	return p
}
