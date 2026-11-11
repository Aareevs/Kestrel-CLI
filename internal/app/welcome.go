package app

import (
	"context"
	"os"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/appdir"
	"github.com/Aareevs/Kestrel-CLI/internal/storage"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// welcomeBudget bounds the project scan behind the welcome screen: it runs
// every time someone types `kestrel`, so it must stay quick.
const welcomeBudget = 400 * time.Millisecond

// Welcome gathers the welcome screen's summary. It only reads: it scans the
// project with a short budget and looks at the session store only if one
// already exists, so typing `kestrel` never creates files or runs anything.
func (a *App) Welcome(ctx context.Context, version string) *api.Welcome {
	w := &api.Welcome{Version: version, Root: a.ProjectRoot, GeneratedAt: a.Now()}
	if res, err := a.Inspect(ctx, InspectOptions{Budget: welcomeBudget}); err == nil {
		w.Technologies = res.Technologies
		w.Services = len(res.Candidates)
		w.ConfigFile = res.ConfigFile
	}
	if dir, _, err := appdir.Resolve(); err == nil {
		if _, err := os.Stat(storage.DBPath(dir)); err == nil {
			if list, err := a.SessionsList(ctx, false, 1); err == nil && len(list.Sessions) > 0 {
				w.LastSession = &list.Sessions[0]
			}
		}
	}
	w.Tips = welcomeTips(w)
	return w
}

// welcomeTips picks next steps that fit where the project is: no config yet,
// nothing recorded yet, a failed last run, or all quiet.
func welcomeTips(w *api.Welcome) []api.WelcomeTip {
	var tips []api.WelcomeTip
	add := func(cmd, why string) { tips = append(tips, api.WelcomeTip{Command: cmd, Why: why}) }
	ls := w.LastSession
	switch {
	case w.ConfigFile == "" && w.Services > 0:
		add("kestrel init", "write a kestrel.yaml for the services found here")
	case w.ConfigFile == "" && ls == nil:
		add("kestrel inspect", "see what Kestrel finds in this directory")
	}
	if ls == nil {
		if w.ConfigFile != "" || w.Services > 0 {
			add("kestrel watch", "start your services and record what they print")
		}
		add("kestrel run -- <command>", "record one command, then ask why it failed")
	} else {
		if ls.Errors > 0 || ls.Status == api.SessionInterrupted {
			add("kestrel why", "explain the failure in the last session")
			add("kestrel diagnose -i", "pick a session and incident from a list")
			add("kestrel verify", "check whether your fix worked")
		} else {
			add("kestrel watch", "record another session")
			add("kestrel sessions", "look back at earlier sessions")
		}
	}
	add("kestrel --help", "every command")
	if len(tips) > 4 {
		tips = tips[:4]
	}
	return tips
}
