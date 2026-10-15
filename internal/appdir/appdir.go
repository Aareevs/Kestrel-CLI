// Package appdir locates Kestrel's per-user data directory, which is never
// inside the user's project.
package appdir

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EnvOverride names the variable that overrides the data directory.
const EnvOverride = "KESTREL_HOME"

// Resolve returns the data directory and whether it came from KESTREL_HOME.
func Resolve() (dir string, overridden bool, err error) {
	home, _ := os.UserHomeDir()
	return resolve(runtime.GOOS, os.Getenv, home)
}

func resolve(goos string, getenv func(string) string, home string) (string, bool, error) {
	if v := getenv(EnvOverride); v != "" {
		abs, err := filepath.Abs(v)
		return abs, true, err
	}
	switch goos {
	case "darwin":
		if home == "" {
			return "", false, errors.New("cannot determine home directory; set " + EnvOverride)
		}
		return filepath.Join(home, "Library", "Application Support", "Kestrel"), false, nil
	case "windows":
		if v := getenv("LOCALAPPDATA"); v != "" {
			return filepath.Join(v, "Kestrel"), false, nil
		}
		return "", false, errors.New("LOCALAPPDATA is not set; set " + EnvOverride)
	default:
		xdg := getenv("XDG_DATA_HOME")
		xdgAbs := filepath.IsAbs(xdg) || (goos != "windows" && strings.HasPrefix(xdg, "/"))
		if xdg != "" && xdgAbs {
			return filepath.Join(xdg, "kestrel"), false, nil
		}
		if home == "" {
			return "", false, errors.New("cannot determine home directory; set " + EnvOverride)
		}
		return filepath.Join(home, ".local", "share", "kestrel"), false, nil
	}
}
