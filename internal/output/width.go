package output

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// TerminalWidth returns the column count of w when it is an interactive
// terminal, and 0 otherwise (pipes, files, tests). Width-aware tidying only
// happens for a known width, so redirected output is never shortened.
func TerminalWidth(w any) int {
	f, ok := w.(*os.File)
	if !ok || !IsTerminal(f) {
		return 0
	}
	cols, _, err := term.GetSize(int(f.Fd()))
	if err != nil || cols <= 0 {
		return 0
	}
	return cols
}

// Fit shortens s to at most max columns, ending in an ellipsis when it cuts.
// It counts runes (not bytes), and works on plain text: style it afterwards.
// A max of 0 or less means no limit.
func Fit(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	r := []rune(s)
	return strings.TrimRight(string(r[:max-1]), " ") + "…"
}

// Tidy makes a path-heavy message easier to read for a person: the project
// root becomes "." and the home directory "~". It only rewrites whole path
// prefixes (so /srv/appx is not touched by a root of /srv/app).
func Tidy(msg, root, home string) string {
	for _, p := range []struct{ prefix, repl string }{{root, "."}, {home, "~"}} {
		if p.prefix == "" || p.prefix == "/" {
			continue
		}
		var b strings.Builder
		rest := msg
		for {
			i := strings.Index(rest, p.prefix)
			if i < 0 {
				b.WriteString(rest)
				break
			}
			after := rest[i+len(p.prefix):]
			// A whole path element: followed by a separator, a quote, a space, or the end.
			if after == "" || strings.ContainsRune("/\\ \"':;,)", rune(after[0])) {
				b.WriteString(rest[:i])
				b.WriteString(p.repl)
			} else {
				b.WriteString(rest[:i+len(p.prefix)])
			}
			rest = after
		}
		msg = b.String()
	}
	return msg
}

// Wrap breaks text into lines of at most width columns at word boundaries. A
// single word longer than width stays whole (a path or URL is never split). A
// width of 0 or less leaves the text on one line.
func Wrap(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	if width <= 0 {
		return []string{strings.Join(words, " ")}
	}
	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(w) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	return append(lines, cur)
}

// wrapHang breaks text at word boundaries: the first line may be w1 columns,
// later lines w2 (they sit under a deeper indent). A width of 0 or less means
// one line.
func wrapHang(text string, w1, w2 int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	if w1 <= 0 || w2 <= 0 {
		return []string{strings.Join(words, " ")}
	}
	var lines []string
	cur, limit := words[0], w1
	for _, w := range words[1:] {
		if utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(w) > limit {
			lines = append(lines, cur)
			cur, limit = w, w2
			continue
		}
		cur += " " + w
	}
	return append(lines, cur)
}

// wrapped writes text under a first-line prefix, wrapping to the terminal
// width with continuation lines indented to hang under the text. With no known
// width it writes one line, exactly as before.
func wrapped(w io.Writer, first, rest string, text string, o TextOptions, style func(string) string) {
	w1, w2 := 0, 0
	if o.Width > 0 {
		w1 = max(o.Width-visibleLen(first), 30)
		w2 = max(o.Width-visibleLen(rest), 30)
	}
	for i, line := range wrapHang(text, w1, w2) {
		prefix := first
		if i > 0 {
			prefix = rest
		}
		fmt.Fprintln(w, prefix+style(line))
	}
}

func plain(s string) string { return s }

// Paragraph writes text word-wrapped to the terminal width (one line when the
// width isn't known), each line styled.
func Paragraph(w io.Writer, text string, o TextOptions, style func(string) string) {
	wrapped(w, "", "", text, o, style)
}

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// visibleLen is the number of columns s takes on a terminal: styling codes
// take none.
func visibleLen(s string) int { return utf8.RuneCountInString(ansiSeq.ReplaceAllString(s, "")) }
