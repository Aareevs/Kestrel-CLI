// Package buildinfo exposes version metadata set at link time:
//
//	go build -ldflags "-X github.com/Aareevs/Kestrel-CLI/internal/buildinfo.Version=v1.0.0 ..."
package buildinfo

import (
	"runtime"
	"runtime/debug"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Set by -ldflags at release time.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Info returns version metadata, falling back to VCS data embedded by the Go
// toolchain for builds made without ldflags.
func Info() api.VersionInfo {
	v := api.VersionInfo{
		SchemaVersion: api.SchemaVersion,
		Version:       Version,
		Commit:        Commit,
		Date:          Date,
		GoVersion:     runtime.Version(),
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v.Version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v.Version = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if v.Commit == "" && len(s.Value) >= 12 {
					v.Commit = s.Value[:12]
				}
			case "vcs.time":
				if v.Date == "" {
					v.Date = s.Value
				}
			}
		}
	}
	if v.Commit == "" {
		v.Commit = "unknown"
	}
	if v.Date == "" {
		v.Date = "unknown"
	}
	return v
}
