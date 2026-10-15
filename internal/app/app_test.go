package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// One project must have one identity however it is reached, because sessions
// are keyed by project root.
func TestProjectRootIsCanonical(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(real)

	viaFlag, err := New(link, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	viaCwd, err := New("", link)
	if err != nil {
		t.Fatal(err)
	}
	if viaFlag.ProjectRoot != want || viaCwd.ProjectRoot != want {
		t.Fatalf("flag=%q cwd=%q want=%q", viaFlag.ProjectRoot, viaCwd.ProjectRoot, want)
	}
}
