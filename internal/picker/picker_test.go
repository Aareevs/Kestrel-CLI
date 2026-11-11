package picker

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

type plainStyle struct{}

func (plainStyle) Bold(s string) string { return s }
func (plainStyle) Dim(s string) string  { return s }
func (plainStyle) Cyan(s string) string { return s }

func items(n int) []Item {
	out := make([]Item, n)
	for i := range out {
		out[i] = Item{Label: fmt.Sprintf("item-%d", i+1), Detail: fmt.Sprintf("detail for item %d, which is rather long and keeps going", i+1)}
	}
	return out
}

func keys(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }

func TestDecodeKey(t *testing.T) {
	tests := []struct {
		in   string
		want decoded
	}{
		{"\r", decoded{k: keyEnter}}, {"\n", decoded{k: keyEnter}},
		{"q", decoded{k: keyCancel}}, {"\x03", decoded{k: keyCancel}}, {"\x04", decoded{k: keyCancel}},
		{"\x1b", decoded{k: keyCancel}}, // a lone ESC is Escape
		{"\x1b[A", decoded{k: keyUp}}, {"\x1b[B", decoded{k: keyDown}},
		{"\x1bOA", decoded{k: keyUp}}, {"\x1bOB", decoded{k: keyDown}}, // application cursor mode
		{"k", decoded{k: keyUp}}, {"j", decoded{k: keyDown}},
		{"\x1b[5~", decoded{k: keyPageUp}}, {"\x1b[6~", decoded{k: keyPageDown}},
		{"\x1b[H", decoded{k: keyHome}}, {"\x1b[F", decoded{k: keyEnd}},
		{"\x1b[1~", decoded{k: keyHome}}, {"\x1b[4~", decoded{k: keyEnd}},
		{"g", decoded{k: keyHome}}, {"G", decoded{k: keyEnd}},
		{"3", decoded{k: keyDigit, digit: 3}}, {"0", decoded{k: keyNone}},
		{"z", decoded{k: keyNone}}, {"\x1b[Z", decoded{k: keyNone}},
	}
	for _, tt := range tests {
		got, err := decodeKey(keys(tt.in))
		if err != nil || got != tt.want {
			t.Errorf("decodeKey(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
		}
	}
	if _, err := decodeKey(keys("")); !errors.Is(err, io.EOF) {
		t.Errorf("empty input: %v", err)
	}
	// Keys are read one at a time from a stream.
	r := keys("jj\r")
	for i, want := range []key{keyDown, keyDown, keyEnter} {
		if d, _ := decodeKey(r); d.k != want {
			t.Errorf("key %d = %v, want %v", i, d.k, want)
		}
	}
}

func TestModelCursorAndScrolling(t *testing.T) {
	m := newModel(20, 5, 0)
	steps := []struct {
		d           decoded
		cursor, top int
	}{
		{decoded{k: keyDown}, 1, 0},
		{decoded{k: keyUp}, 0, 0},
		{decoded{k: keyUp}, 0, 0}, // stops at the top
		{decoded{k: keyPageDown}, 5, 1},
		{decoded{k: keyEnd}, 19, 15},
		{decoded{k: keyDown}, 19, 15}, // stops at the bottom
		{decoded{k: keyPageUp}, 14, 14},
		{decoded{k: keyHome}, 0, 0},
		{decoded{k: keyDigit, digit: 4}, 3, 0},
		{decoded{k: keyDigit, digit: 9}, 8, 4},
	}
	for i, s := range steps {
		m.apply(s.d)
		if m.cursor != s.cursor || m.top != s.top {
			t.Fatalf("step %d: cursor %d top %d, want %d/%d", i, m.cursor, m.top, s.cursor, s.top)
		}
	}
	// A list shorter than the window, and a start position past the end.
	if m := newModel(3, 12, 0); m.height != 3 {
		t.Errorf("height = %d, want 3", m.height)
	}
	if m := newModel(10, 4, 99); m.cursor != 9 || m.top != 6 {
		t.Errorf("initial past the end: cursor %d top %d", m.cursor, m.top)
	}
}

var esc = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func TestRenderMarksTheCursorAndScrollsAndFits(t *testing.T) {
	o := Options{Title: "Pick a session", Items: items(30), Style: plainStyle{}, Width: 50}
	m := newModel(30, 6, 0)
	lines := render(m, o)
	if lines[0] != "Pick a session" || !strings.HasPrefix(lines[1], "❯ ") || strings.HasPrefix(lines[2], "❯") {
		t.Fatalf("frame:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "↓ 24 more") || strings.Contains(strings.Join(lines, "\n"), "↑") && !strings.Contains(lines[len(lines)-1], "↑↓") {
		t.Fatalf("scroll hints:\n%s", strings.Join(lines, "\n"))
	}
	m.apply(decoded{k: keyEnd})
	lines = render(m, o)
	if !strings.Contains(strings.Join(lines, "\n"), "↑ 24 more") || !strings.Contains(strings.Join(lines, "\n"), "item-30") {
		t.Fatalf("end of the list:\n%s", strings.Join(lines, "\n"))
	}
	for _, l := range lines {
		if n := utf8.RuneCountInString(l); n > 50 {
			t.Errorf("line is %d columns on a 50-column terminal: %q", n, l)
		}
	}
	// Without a width nothing is cut.
	o.Width = 0
	if !strings.Contains(strings.Join(render(newModel(30, 6, 0), o), "\n"), "which is rather long and keeps going") {
		t.Error("detail was shortened without a known width")
	}
}

func pick(t *testing.T, input string, n int) (int, string, error) {
	t.Helper()
	var out bytes.Buffer
	i, err := Pick(strings.NewReader(input), &out, Options{Title: "t", Items: items(n), Style: plainStyle{}})
	return i, out.String(), err
}

func TestPick(t *testing.T) {
	tests := []struct {
		name, input string
		want        int
		err         error
	}{
		{"enter takes the first", "\r", 0, nil},
		{"arrows", "\x1b[B\x1b[B\r", 2, nil},
		{"vim keys", "jjjk\r", 2, nil},
		{"digit then enter", "4\r", 3, nil},
		{"end then enter", "G\r", 4, nil},
		{"page down is clamped", "\x1b[6~\x1b[6~\x1b[6~\r", 4, nil},
		{"unknown keys are ignored", "zzz\x00\r", 0, nil},
		{"q cancels", "jq", 0, ErrCancelled},
		{"ctrl-c cancels", "j\x03", 0, ErrCancelled},
		{"escape cancels", "\x1b", 0, ErrCancelled},
		{"end of input cancels", "jj", 0, ErrCancelled},
	}
	for _, tt := range tests {
		i, out, err := pick(t, tt.input, 5)
		if !errors.Is(err, tt.err) || (err == nil && i != tt.want) {
			t.Errorf("%s: got %d, %v; want %d, %v", tt.name, i, err, tt.want, tt.err)
		}
		// The terminal is always left as it was found: the cursor is shown again
		// and the frame is erased.
		if !strings.HasSuffix(out, "\x1b[?25h") || strings.Count(out, "\x1b[?25l") != 1 {
			t.Errorf("%s: cursor not restored: %q", tt.name, out)
		}
	}
	if _, _, err := pick(t, "\r", 0); err == nil {
		t.Error("an empty list is an error, not a hang")
	}
}

// The frame is redrawn in place: each redraw first moves back up over the
// previous frame and clears below, and the final erase removes the last one.
func TestPickRedrawsInPlace(t *testing.T) {
	_, out, _ := pick(t, "j\r", 3)
	frame := 1 + 3 + 1 // title, rows, help line
	up := fmt.Sprintf("\x1b[%dF\x1b[J", frame)
	if strings.Count(out, up) != 2 { // one redraw after j, one erase after enter
		t.Fatalf("expected two moves back over a %d-line frame:\n%q", frame, out)
	}
	frames := strings.Split(esc.ReplaceAllString(out, "\x00"), "\x00")
	var seen []string
	for _, f := range frames {
		if strings.Contains(f, "❯") {
			for _, l := range strings.Split(f, "\n") {
				if strings.Contains(l, "❯") {
					seen = append(seen, strings.Fields(l)[2])
				}
			}
		}
	}
	if strings.Join(seen, ",") != "item-1,item-2" {
		t.Fatalf("the cursor should have been drawn on item-1 then item-2, saw %v", seen)
	}
}

func TestPickRawModeIsEnteredAndAlwaysRestored(t *testing.T) {
	var events []string
	raw := func() (func(), error) {
		events = append(events, "raw")
		return func() { events = append(events, "restore") }, nil
	}
	var out bytes.Buffer
	if _, err := Pick(strings.NewReader("q"), &out, Options{Items: items(2), Style: plainStyle{}, Raw: raw}); !errors.Is(err, ErrCancelled) {
		t.Fatal(err)
	}
	if strings.Join(events, ",") != "raw,restore" {
		t.Fatalf("events = %v", events)
	}
	// A terminal that can't enter raw mode is an error, and nothing is drawn.
	out.Reset()
	bad := func() (func(), error) { return nil, errors.New("not a terminal") }
	if _, err := Pick(strings.NewReader("\r"), &out, Options{Items: items(2), Style: plainStyle{}, Raw: bad}); err == nil || out.Len() != 0 {
		t.Fatalf("err %v, output %q", err, out.String())
	}
}
