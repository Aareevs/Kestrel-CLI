package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/Aareevs/Kestrel-CLI/internal/appdir"
	"github.com/Aareevs/Kestrel-CLI/internal/config"
	"github.com/Aareevs/Kestrel-CLI/internal/storage"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Doctor runs Kestrel's self-checks. It does not create directories; it only
// probes writability with a temporary file that it removes again.
func (a *App) Doctor(ctx context.Context) *api.DoctorReport {
	r := &api.DoctorReport{SchemaVersion: api.SchemaVersion}
	r.Checks = append(r.Checks,
		checkPlatform(runtime.GOOS, runtime.GOARCH),
		checkDataDir(),
		a.checkConfig(),
		a.checkStorage(ctx),
		checkTool("lsof", "used in suggested next steps for port conflicts"),
	)
	r.OK = true
	for _, c := range r.Checks {
		if c.Status == api.CheckFail {
			r.OK = false
		}
	}
	return r
}

func checkPlatform(goos, goarch string) api.DoctorCheck {
	c := api.DoctorCheck{Name: "platform"}
	switch goos {
	case "darwin", "linux":
		c.Status, c.Message = api.CheckOK, fmt.Sprintf("%s/%s supported (process groups via setpgid)", goos, goarch)
	case "windows":
		c.Status, c.Message = api.CheckOK, fmt.Sprintf("%s/%s supported (process supervision via Job Objects)", goos, goarch)
	default:
		c.Status, c.Message = api.CheckWarn, fmt.Sprintf("%s/%s is not an officially supported platform", goos, goarch)
	}
	return c
}

func checkDataDir() api.DoctorCheck {
	c := api.DoctorCheck{Name: "data dir"}
	dir, overridden, err := appdir.Resolve()
	if err != nil {
		c.Status, c.Message = api.CheckFail, err.Error()
		return c
	}
	origin := ""
	if overridden {
		origin = " (from " + appdir.EnvOverride + ")"
	}
	fi, err := os.Stat(dir)
	switch {
	case err == nil && !fi.IsDir():
		c.Status, c.Message = api.CheckFail, dir+origin+" exists but is not a directory"
		return c
	case err == nil:
		if werr := probeWritable(dir); werr != nil {
			c.Status, c.Message = api.CheckFail, fmt.Sprintf("%s%s is not writable: %v", dir, origin, werr)
			c.Hint = "fix the permissions or set " + appdir.EnvOverride + " to a writable directory"
			return c
		}
		c.Status, c.Message = api.CheckOK, dir+origin
		return c
	case errors.Is(err, os.ErrNotExist):
		parent := filepath.Dir(dir)
		for {
			if _, perr := os.Stat(parent); perr == nil {
				break
			}
			next := filepath.Dir(parent)
			if next == parent {
				break
			}
			parent = next
		}
		if werr := probeWritable(parent); werr != nil {
			c.Status, c.Message = api.CheckFail, fmt.Sprintf("%s%s cannot be created: %s is not writable", dir, origin, parent)
			c.Hint = "set " + appdir.EnvOverride + " to a writable directory"
			return c
		}
		c.Status, c.Message = api.CheckOK, dir+origin+" (will be created on first use)"
		return c
	default:
		c.Status, c.Message = api.CheckFail, fmt.Sprintf("%s%s: %v", dir, origin, err)
		return c
	}
}

func (a *App) checkStorage(ctx context.Context) api.DoctorCheck {
	c := api.DoctorCheck{Name: "storage"}
	dir, _, err := appdir.Resolve()
	if err != nil {
		c.Status, c.Message = api.CheckSkip, "data directory unknown"
		return c
	}
	h := storage.Inspect(ctx, dir)
	switch {
	case errors.Is(h.Problem, storage.ErrCorrupt):
		c.Status, c.Message = api.CheckFail, fmt.Sprintf("%s: %v", h.Path, h.Problem)
		c.Hint = "Kestrel left the file untouched. Move it aside (e.g. rename kestrel.db) to start fresh; sessions in it can't be read."
	case errors.Is(h.Problem, storage.ErrTooNew):
		c.Status, c.Message = api.CheckFail, h.Problem.Error()
		c.Hint = "upgrade Kestrel, or point KESTREL_HOME at a different directory"
	case h.Problem != nil:
		c.Status, c.Message = api.CheckFail, fmt.Sprintf("%s: %v", h.Path, h.Problem)
	case !h.Exists:
		c.Status, c.Message = api.CheckOK, "no sessions recorded yet (the database is created on the first run/watch)"
	case h.WriteErrors > 0:
		c.Status = api.CheckWarn
		c.Message = fmt.Sprintf("%d session(s), schema v%d; %d session(s) have incomplete records", h.Sessions, h.SchemaVersion, h.WriteErrors)
		c.Hint = "storage failed during those sessions (disk full or permissions); see 'kestrel sessions list'"
	default:
		c.Status = api.CheckOK
		c.Message = fmt.Sprintf("%d session(s), schema v%d, database %s", h.Sessions, h.SchemaVersion, humanSize(h.DiskBytes))
	}
	return c
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".kestrel-doctor-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func (a *App) checkConfig() api.DoctorCheck {
	c := api.DoctorCheck{Name: "config"}
	cfg, p, err := a.LoadConfig()
	var ve *config.ValidationError
	switch {
	case errors.Is(err, ErrNoConfig):
		c.Status, c.Message = api.CheckOK, "no kestrel.yaml in "+a.ProjectRoot+" (zero-config mode)"
	case errors.As(err, &ve):
		c.Status = api.CheckWarn
		c.Message = fmt.Sprintf("%s has %d problem(s)", a.rel(p), len(ve.Issues))
		c.Hint = "run: kestrel config validate"
	case err != nil:
		c.Status, c.Message = api.CheckWarn, err.Error()
	default:
		c.Status, c.Message = api.CheckOK, fmt.Sprintf("%s is valid (%d services)", a.rel(p), len(cfg.Services))
	}
	return c
}

func checkTool(name, why string) api.DoctorCheck {
	c := api.DoctorCheck{Name: name}
	if runtime.GOOS == "windows" {
		c.Status, c.Message = api.CheckSkip, "not used on Windows"
		return c
	}
	if p, err := exec.LookPath(name); err == nil {
		c.Status, c.Message = api.CheckOK, p
		return c
	}
	c.Status, c.Message, c.Hint = api.CheckWarn, "not found on PATH", why
	return c
}
