package integration

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type junitDoc struct {
	Tests    int `xml:"tests,attr"`
	Failures int `xml:"failures,attr"`
	Skipped  int `xml:"skipped,attr"`
	Suite    struct {
		Props []struct {
			Name  string `xml:"name,attr"`
			Value string `xml:"value,attr"`
		} `xml:"properties>property"`
		Cases []struct {
			Name    string `xml:"name,attr"`
			Failure *struct {
				Type string `xml:"type,attr"`
				Text string `xml:",chardata"`
			} `xml:"failure"`
			Skipped *struct {
				Message string `xml:"message,attr"`
			} `xml:"skipped"`
		} `xml:"testcase"`
	} `xml:"testsuite"`
}

func parseJUnit(t *testing.T, s string) junitDoc {
	t.Helper()
	var d junitDoc
	if err := xml.Unmarshal([]byte(s), &d); err != nil {
		t.Fatalf("not well-formed JUnit: %v\n%s", err, s)
	}
	return d
}

func prop(d junitDoc, name string) string {
	for _, p := range d.Suite.Props {
		if p.Name == name {
			return p.Value
		}
	}
	return ""
}

// The CI reports from a real run: on stdout with --format, and as files with
// --markdown / --junit, written even when the verdict fails, with the same exit
// code as ever.
func TestVerifyCIReports(t *testing.T) {
	home := t.TempDir()
	okCmd, failCmd := "echo fine", "echo 'FAIL: login with token=sup3rs3cret'; exit 3"
	if runtime.GOOS == "windows" {
		okCmd, failCmd = "echo fine", "echo FAIL: login with token=sup3rs3cret & exit 3"
	}
	good := execProject(t, "    - { type: command, run: \""+okCmd+"\", timeout: 5s }\n")
	bad := execProject(t, "    - { type: command, run: \""+okCmd+"\", timeout: 5s }\n    - { type: command, run: \""+strings.ReplaceAll(failCmd, `"`, `\"`)+"\", timeout: 5s }\n")
	common := []string{"--window", "500ms", "--interval", "250ms", "--allow-exec"}

	// Resolved: exit 0; JUnit and Markdown on stdout.
	code, out, errs := kestrelCmd(t, home, good, append([]string{"verify", "--format", "junit"}, common...)...)
	d := parseJUnit(t, out)
	if code != 0 || d.Failures != 0 || prop(d, "verdict") != "resolved" || prop(d, "claim") != "verified" || len(d.Suite.Cases) < 2 {
		t.Fatalf("junit on stdout: exit %d\n%s%s", code, out, errs)
	}
	code, out, errs = kestrelCmd(t, home, good, append([]string{"verify", "--format", "markdown"}, common...)...)
	if code != 0 || !strings.HasPrefix(out, "## ✅ Kestrel verify: resolved") || !strings.Contains(out, "| ✅ |") || strings.Contains(out, "\x1b[") {
		t.Fatalf("markdown on stdout: exit %d\n%s%s", code, out, errs)
	}
	// Progress goes to stderr, so stdout is only the report.
	if !strings.Contains(errs, "Verifying") {
		t.Errorf("progress should be on stderr: %q", errs)
	}

	// Failing: exit 1, and files are written (over an existing one) alongside normal output.
	dir := t.TempDir()
	mdPath, xmlPath := filepath.Join(dir, "summary.md"), filepath.Join(dir, "junit.xml")
	os.WriteFile(mdPath, []byte("stale contents"), 0o644)
	code, out, errs = kestrelCmd(t, home, bad, append([]string{"verify", "--no-color", "--markdown", mdPath, "--junit", xmlPath}, common...)...)
	if code != 1 || !strings.Contains(out, "Verdict:") {
		t.Fatalf("console output should stay the text report: exit %d\n%s%s", code, out, errs)
	}
	md, _ := os.ReadFile(mdPath)
	x, _ := os.ReadFile(xmlPath)
	if !strings.Contains(string(md), "Kestrel verify: partially_resolved") || strings.Contains(string(md), "stale") {
		t.Errorf("markdown file:\n%s", md)
	}
	d = parseJUnit(t, string(x))
	if d.Failures != 1 || prop(d, "verdict") != "partially_resolved" {
		t.Fatalf("junit file: %d failures, verdict %q\n%s", d.Failures, prop(d, "verdict"), x)
	}
	// The failing command's output is there, redacted like everything else.
	if all := string(md) + string(x); strings.Contains(all, "sup3rs3cret") || !strings.Contains(string(x), "FAIL: login with token=[REDACTED") {
		t.Errorf("command output must be redacted in the reports:\n%s\n%s", md, x)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".kestrel-report-*")); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}

	// Not run (no --allow-exec, no terminal): a skipped test case, verdict inconclusive, exit 3.
	code, out, _ = kestrelCmd(t, home, bad, "verify", "--format", "junit", "--window", "500ms", "--interval", "250ms")
	d = parseJUnit(t, out)
	if code != 3 || d.Skipped != 2 || prop(d, "verdict") != "inconclusive" {
		t.Fatalf("skipped commands: exit %d, %d skipped, verdict %q\n%s", code, d.Skipped, prop(d, "verdict"), out)
	}
	if !strings.Contains(out, "--allow-exec") {
		t.Errorf("the skip reason should say how to run it:\n%s", out)
	}

	// --json still prints JSON, and the files are written too.
	code, out, _ = kestrelCmd(t, home, good, append([]string{"verify", "--json", "--junit", xmlPath}, common...)...)
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("--json with --junit: exit %d\n%s", code, out)
	}
	if x, _ := os.ReadFile(xmlPath); prop(parseJUnit(t, string(x)), "verdict") != "resolved" {
		t.Errorf("the junit file should be the new run's:\n%s", x)
	}
}

func TestVerifyCIReportErrors(t *testing.T) {
	home := t.TempDir()
	good := execProject(t, "    - { type: command, run: \"echo fine\", timeout: 5s }\n")
	// These formats exist only for verify.
	for _, args := range [][]string{{"diagnose", "--format", "junit"}, {"timeline", "--format", "markdown"}, {"inspect", "--format", "junit"}} {
		code, _, errs := kestrelCmd(t, home, good, args...)
		if code != 2 || !strings.Contains(errs, "only for `kestrel verify`") {
			t.Errorf("%v: exit %d\n%s", args, code, errs)
		}
	}
	if code, _, errs := kestrelCmd(t, home, good, "verify", "--format", "xml"); code != 2 || !strings.Contains(errs, "markdown or junit") {
		t.Errorf("unknown format: exit %d\n%s", code, errs)
	}
	// A report path in a directory that doesn't exist is an error that names the path, not a silent skip.
	missing := filepath.Join(t.TempDir(), "no-such-dir", "junit.xml")
	code, _, errs := kestrelCmd(t, home, good, "verify", "--junit", missing, "--allow-exec", "--window", "500ms", "--interval", "250ms")
	if code != 4 || !strings.Contains(errs, missing) {
		t.Errorf("unwritable report: exit %d\n%s", code, errs)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("the report should not exist")
	}
}
