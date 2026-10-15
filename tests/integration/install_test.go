//go:build !windows

package integration

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRelease lays out a release directory the way GoReleaser does
// (kestrel_<version>_<os>_<arch>.tar.gz plus checksums.txt) around the
// freshly built kestrel binary.
func fakeRelease(t *testing.T, version string) (dir, archive string) {
	t.Helper()
	k, _ := buildBins(t)
	dir = filepath.Join(os.TempDir(), fmt.Sprintf("kestrel-rel-%d", os.Getpid())) // no spaces: it becomes a file:// URL
	os.RemoveAll(dir)
	os.MkdirAll(dir, 0o755)
	t.Cleanup(func() { os.RemoveAll(dir) })
	archive = fmt.Sprintf("kestrel_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)

	bin, err := os.ReadFile(k)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Create(filepath.Join(dir, archive))
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range map[string][]byte{"kestrel": bin, "LICENSE": []byte("MIT\n")} {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))})
		tw.Write(body)
	}
	tw.Close()
	gz.Close()
	f.Close()

	sum := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return fmt.Sprintf("%x", sha256.Sum256(b))
	}
	os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(sum(archive)+"  "+archive+"\n"), 0o644)
	return dir, archive
}

func runInstaller(t *testing.T, env ...string) (int, string) {
	t.Helper()
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("sh", filepath.Join(root, "scripts", "install.sh"))
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, string(out)
}

func TestInstallScript(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("needs curl")
	}
	dir, archive := fakeRelease(t, "1.2.3")
	dest := filepath.Join(t.TempDir(), "bin")
	env := []string{"KESTREL_VERSION=v1.2.3", "KESTREL_DOWNLOAD_BASE=file://" + dir, "KESTREL_INSTALL_DIR=" + dest}

	code, out := runInstaller(t, env...)
	if code != 0 || !strings.Contains(out, "checksum verified") {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	if v, err := exec.Command(filepath.Join(dest, "kestrel"), "version").Output(); err != nil || !strings.HasPrefix(string(v), "kestrel ") {
		t.Fatalf("installed binary doesn't run: %v %s", err, v)
	}

	// A tampered archive is refused, and nothing is installed.
	f, _ := os.OpenFile(filepath.Join(dir, archive), os.O_APPEND|os.O_WRONLY, 0)
	f.Write([]byte("x"))
	f.Close()
	dest2 := filepath.Join(t.TempDir(), "bin")
	code, out = runInstaller(t, "KESTREL_VERSION=1.2.3", "KESTREL_DOWNLOAD_BASE=file://"+dir, "KESTREL_INSTALL_DIR="+dest2)
	if code == 0 || !strings.Contains(out, "checksum mismatch") {
		t.Fatalf("tampered archive: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dest2, "kestrel")); err == nil {
		t.Fatal("a tampered archive was installed")
	}

	// A version that doesn't exist fails cleanly.
	if code, out = runInstaller(t, "KESTREL_VERSION=v9.9.9", "KESTREL_DOWNLOAD_BASE=file://"+dir, "KESTREL_INSTALL_DIR="+dest2); code == 0 || !strings.Contains(out, "download failed") {
		t.Fatalf("missing version: exit %d\n%s", code, out)
	}
}

// The Homebrew formula is rendered from a release's checksums.txt: every
// platform's URL and checksum come from it, and a missing archive is an error.
func TestHomebrewFormula(t *testing.T) {
	root, _ := filepath.Abs("../..")
	script := filepath.Join(root, "scripts", "homebrew-formula.sh")
	dir := t.TempDir()
	sums := filepath.Join(dir, "checksums.txt")
	var lines []string
	want := map[string]string{}
	for i, p := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
		sum := strings.Repeat(fmt.Sprintf("%x", i+1), 64)
		name := "kestrel_1.2.3_" + p + ".tar.gz"
		lines = append(lines, sum+"  "+name)
		want[name] = sum
	}
	os.WriteFile(sums, []byte(strings.Join(lines, "\n")+"\n"), 0o644)

	out, err := exec.Command("sh", script, "v1.2.3", sums).Output()
	if err != nil {
		t.Fatal(err)
	}
	f := string(out)
	for name, sum := range want {
		url := "https://github.com/Aareevs/Kestrel-CLI/releases/download/v1.2.3/" + name
		i := strings.Index(f, url)
		if i < 0 || !strings.Contains(f[i:], `sha256 "`+sum+`"`) || strings.Index(f[i:], sum) > 200 {
			t.Errorf("%s: URL or its checksum missing\n%s", name, f)
		}
	}
	if !strings.Contains(f, "class Kestrel < Formula") || !strings.Contains(f, `version "1.2.3"`) || !strings.Contains(f, `bin.install "kestrel"`) {
		t.Fatalf("formula:\n%s", f)
	}
	if ruby, err := exec.LookPath("ruby"); err == nil {
		rb := filepath.Join(dir, "kestrel.rb")
		os.WriteFile(rb, out, 0o644)
		if o, err := exec.Command(ruby, "-c", rb).CombinedOutput(); err != nil {
			t.Fatalf("not valid Ruby: %s", o)
		}
	}

	// An archive missing from checksums.txt fails instead of producing a
	// formula with an empty checksum.
	os.WriteFile(sums, []byte(strings.Join(lines[:3], "\n")+"\n"), 0o644)
	if err := exec.Command("sh", script, "v1.2.3", sums).Run(); err == nil {
		t.Fatal("a missing archive must fail")
	}
}
