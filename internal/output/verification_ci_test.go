package output

import (
	"bytes"
	"encoding/xml"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run: go test ./internal/output -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from its golden (go test ./internal/output -update, then review the diff)\n--- got\n%s", name, got)
	}
}

func exit(n int) *int { return &n }

var ciT0 = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func baseReport(v api.Verdict, claim api.ClaimLevel) *api.VerificationReport {
	return &api.VerificationReport{
		SchemaVersion: 1, ID: "V1", Verdict: v, Claim: claim, Window: "30s", Interval: "2s",
		StartedAt: ciT0, EndedAt: ciT0.Add(31 * time.Second),
		Session:  &api.SessionSummary{ID: "01M76TESTSESSION"},
		Incident: &api.Incident{Number: 2},
		Checks:   []api.CheckResult{}, Before: []api.Evidence{}, Recovery: []api.Evidence{}, After: []api.Evidence{}, Reasons: []string{},
	}
}

func resolvedReport() *api.VerificationReport {
	r := baseReport(api.VerdictResolved, api.ClaimVerified)
	r.Live = true
	r.Checks = []api.CheckResult{
		{Kind: api.CheckKindProbe, Service: "api", Target: "http://127.0.0.1:8080/health", Status: api.CheckPassed, Samples: 15, Passed: 15},
		{Kind: api.CheckKindTCP, Target: "127.0.0.1:5432", Status: api.CheckPassed, Samples: 15, Passed: 15},
		{Kind: api.CheckKindCommand, Target: "go test ./...", Status: api.CheckPassed, ExitCode: exit(0), DurationMS: 4200},
	}
	r.Reasons = []string{"passing on every sample: api (probe http://127.0.0.1:8080/health)", "no new errors from the affected services during the window"}
	return r
}

// A report with everything that can go wrong, and with hostile text where the
// user's programs' output ends up.
func failingReport() *api.VerificationReport {
	r := baseReport(api.VerdictPartiallyResolved, api.ClaimObserved)
	r.Live = true
	r.Checks = []api.CheckResult{
		{Kind: api.CheckKindProbe, Service: "api", Target: "http://127.0.0.1:8080/health", Status: api.CheckPassed, Samples: 15, Passed: 15},
		{Kind: api.CheckKindProbe, Service: "web", Target: "http://127.0.0.1:5173/", Status: api.CheckFailed, Samples: 15, Passed: 0, Detail: "status 503 | body: <script>alert(1)</script> `ticks` [link](http://evil) @everyone"},
		{Kind: api.CheckKindTCP, Target: "127.0.0.1:5432", Status: api.CheckFlaky, Samples: 15, Passed: 9, Detail: "connection refused"},
		{Kind: api.CheckKindCommand, Target: "make test", Status: api.CheckFailed, ExitCode: exit(2), DurationMS: 1500, Output: []string{"FAIL: TestX", "bad \x00 byte & <tag>", "``` escape attempt", "# heading", "@mention"}},
		{Kind: api.CheckKindCommand, Target: "sleep 99", Status: api.CheckTimedOut, Timeout: "2s", DurationMS: 2000},
		{Kind: api.CheckKindCommand, Target: "./slow-check.sh", Status: api.CheckSkipped, SkipReason: "not authorized: pass --allow-exec to run it (no terminal to ask in)"},
		// A service's name can come from a program's file name, so it is data too.
		{Kind: api.CheckKindProbe, Service: "<img src=x onerror=alert(1)>", Target: "http://127.0.0.1:1/", Status: api.CheckSkipped, SkipReason: "skipped: <b>bold</b> | pipe"},
	}
	r.After = []api.Evidence{
		{EventID: "E1", Service: "web", Summary: "web: line one\nline two | <b>bold</b> ```fence``` ", Count: 6},
		{EventID: "E2", Service: "api", Summary: "api: connection refused to 127.0.0.1:5432"},
	}
	r.Before = []api.Evidence{{EventID: "E0", Service: "web", Summary: "web: crashed (exit code 1)"}}
	r.Reasons = []string{"failing: web (probe http://127.0.0.1:5173/), command `make test`", "the affected services logged 7 new error event(s) during the window"}
	r.Notes = []string{"an error repeating unchanged is recorded as a summary every 2s"}
	return r
}

func inconclusiveReport() *api.VerificationReport {
	r := baseReport(api.VerdictInconclusive, api.ClaimUnresolved)
	r.Session, r.Incident = nil, nil
	r.Reasons = []string{"no check could run: configure a health probe or verify.checks, or verify while 'kestrel watch' is running"}
	return r
}

func render(t *testing.T, r *api.VerificationReport) (md, junit string) {
	t.Helper()
	var m, j bytes.Buffer
	VerificationMarkdown(&m, r)
	if err := VerificationJUnit(&j, r); err != nil {
		t.Fatal(err)
	}
	return m.String(), j.String()
}

func TestVerificationCIGoldens(t *testing.T) {
	for name, r := range map[string]*api.VerificationReport{"resolved": resolvedReport(), "failing": failingReport(), "inconclusive": inconclusiveReport()} {
		md, junit := render(t, r)
		golden(t, "verify_"+name+".md", md)
		golden(t, "verify_"+name+".junit.xml", junit)
	}
}

// Whatever the programs printed, the table keeps its shape and nothing renders as markup.
func TestMarkdownTreatsProcessOutputAsData(t *testing.T) {
	md, _ := render(t, failingReport())
	for _, line := range strings.Split(md, "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		// Cells split on pipes that are not escaped: always 4 cells, however hostile the text.
		cells := regexp.MustCompile(`[^\\]\|`).Split(line, -1)
		if n := len(cells); n != 5 { // leading "|", 3 inner separators, trailing "|": "| a | b | c | d |" has 5 pieces
			t.Errorf("row has %d pieces, want 5: %q", n, line)
		}
	}
	// Outside code spans there is only Kestrel's own text: no tag from process output,
	// no link, no mention. (Inside a code span they render as plain characters.)
	outside := stripCodeSpans(md)
	for _, bad := range []string{"<script>", "<b>", "<tag>", "<img", "](http://evil)", "@everyone"} {
		if strings.Contains(outside, bad) {
			t.Errorf("%q from process output appears outside a code span:\n%s", bad, outside)
		}
	}
	// A failing command's output is in a fenced block that its own backticks can't close:
	// everything between the fences is verbatim, and the fence is longer than the longest run inside.
	if !strings.Contains(md, "**Output of command** `make test`\n\n````text\nFAIL: TestX\nbad  byte & <tag>\n``` escape attempt\n# heading\n@mention\n````\n") {
		t.Errorf("command output block missing or not safely fenced:\n%s", md)
	}
	if !strings.Contains(md, "<script>alert(1)</script>") {
		t.Error("test premise: the hostile text should be present, inside a code span")
	}
	// The one tag Kestrel itself writes is intact.
	if !strings.Contains(outside, "<details><summary>") {
		t.Error("expected Kestrel's own <details>")
	}
}

// stripCodeSpans removes inline code spans (a run of backticks up to the next
// run of the same length), the way a Markdown renderer reads them.
func stripCodeSpans(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '`' {
			b.WriteByte(s[i])
			i++
			continue
		}
		n := 0
		for i+n < len(s) && s[i+n] == '`' {
			n++
		}
		end := -1
		for j := i + n; j < len(s); {
			if s[j] != '`' {
				j++
				continue
			}
			m := 0
			for j+m < len(s) && s[j+m] == '`' {
				m++
			}
			if m == n {
				end = j + m
				break
			}
			j += m
		}
		if end < 0 { // an unclosed run is literal text
			b.WriteString(s[i : i+n])
			i += n
			continue
		}
		b.WriteString("␣")
		i = end
	}
	return b.String()
}

func TestMarkdownCodeSpans(t *testing.T) {
	for in, want := range map[string]string{
		"plain":       "`plain`",
		"a | b":       `` + "`a \\| b`",
		"has `tick`":  "`` has `tick` ``", // ends in a backtick: padded so it can't merge with the fence
		"line\nbreak": "`line break`",
		"`edge":       "`` `edge ``",
		"a ```x``` b": "`a ˋˋˋxˋˋˋ b`",
		"two `` here": "`two ˋˋ here`",
		"":            "``",
	} {
		if got := mdCode(in); got != want {
			t.Errorf("mdCode(%q) = %q, want %q", in, got, want)
		}
	}
	if got := mdCode(strings.Repeat("x", 500)); len([]rune(got)) != 162 {
		t.Errorf("long text should be cut to 160 characters, got %d", len([]rune(got)))
	}
}

// The XML is well formed and its counts add up, for every kind of report.
func TestJUnitIsWellFormedAndCountsAddUp(t *testing.T) {
	for name, r := range map[string]*api.VerificationReport{"resolved": resolvedReport(), "failing": failingReport(), "inconclusive": inconclusiveReport()} {
		_, x := render(t, r)
		var got junitSuites
		if err := xml.Unmarshal([]byte(x), &got); err != nil {
			t.Fatalf("%s: not well-formed XML: %v\n%s", name, err, x)
		}
		s := got.Suite[0]
		fail, skip := 0, 0
		for _, c := range s.Cases {
			if c.Failure != nil {
				fail++
			}
			if c.Skipped != nil {
				skip++
			}
		}
		if s.Tests != len(s.Cases) || s.Failures != fail || s.Skipped != skip || got.Tests != s.Tests || got.Failures != s.Failures || got.Skipped != s.Skipped {
			t.Errorf("%s: counts: suite %d/%d/%d, cases %d/%d/%d", name, s.Tests, s.Failures, s.Skipped, len(s.Cases), fail, skip)
		}
		if strings.ContainsRune(x, 0) || strings.Contains(x, "\x00") {
			t.Errorf("%s: control character in XML", name)
		}
	}
	_, x := render(t, failingReport())
	var got junitSuites
	xml.Unmarshal([]byte(x), &got)
	s := got.Suite[0]
	// 5 failures (probe, flaky tcp, two commands, and errors recurring), 2 skipped, 1 passing probe.
	if s.Failures != 5 || s.Skipped != 2 {
		t.Errorf("failing report: %d failures, %d skipped (want 5 and 2)\n%s", s.Failures, s.Skipped, x)
	}
	byName := map[string]junitCase{}
	for _, c := range s.Cases {
		byName[c.Name] = c
	}
	if c := byName["command ./slow-check.sh"]; c.Skipped == nil || !strings.Contains(c.Skipped.Message, "--allow-exec") {
		t.Errorf("skipped command: %+v", c)
	}
	if c := byName["tcp 127.0.0.1:5432"]; c.Failure == nil || c.Failure.Type != "flaky" {
		t.Errorf("flaky check should be a failure of type flaky: %+v", c)
	}
	if c := byName["command make test"]; c.Failure == nil || !strings.Contains(c.Failure.Text, "FAIL: TestX") || !strings.Contains(c.Failure.Text, "bad  byte & <tag>") {
		t.Errorf("command failure text (control characters dropped, markup preserved as text): %+v", c.Failure)
	}
	if c := byName["no new errors from the affected services during the window"]; c.Failure == nil || c.Failure.Message != "7 new error event(s)" {
		t.Errorf("recurrence case: %+v", c)
	}
	props := map[string]string{}
	for _, p := range s.Props {
		props[p.Name] = p.Value
	}
	if props["verdict"] != "partially_resolved" || props["session"] != "01M76TESTSESSION" || props["incident"] != "2" {
		t.Errorf("properties: %v", props)
	}
}
