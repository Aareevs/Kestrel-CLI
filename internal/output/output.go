// Package output renders Kestrel results as human text or stable JSON.
package output

import (
	"encoding/json"
	"io"
	"os"

	"github.com/mattn/go-isatty"
)

// Format is an output format selected by --format.
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// ColorEnabled decides whether to emit ANSI color: never with --no-color,
// NO_COLOR set (any value, per no-color.org), or TERM=dumb; otherwise only
// when w is a terminal.
func ColorEnabled(w io.Writer, noColorFlag bool, getenv func(string) string) bool {
	if noColorFlag || getenv("NO_COLOR") != "" || getenv("TERM") == "dumb" {
		return false
	}
	return IsTerminal(w)
}

// IsTerminal reports whether v is an interactive terminal. A character device
// is not enough: /dev/null is one, and a prompt must never read from it.
func IsTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// Style applies ANSI styles when enabled.
type Style struct{ on bool }

// NewStyle returns a Style that emits color only if on is true.
func NewStyle(on bool) Style { return Style{on: on} }

func (s Style) wrap(code, v string) string {
	if !s.on || v == "" {
		return v
	}
	return "\x1b[" + code + "m" + v + "\x1b[0m"
}

func (s Style) Bold(v string) string   { return s.wrap("1", v) }
func (s Style) Dim(v string) string    { return s.wrap("2", v) }
func (s Style) Red(v string) string    { return s.wrap("31", v) }
func (s Style) Green(v string) string  { return s.wrap("32", v) }
func (s Style) Yellow(v string) string { return s.wrap("33", v) }
func (s Style) Cyan(v string) string   { return s.wrap("36", v) }

// WriteJSON writes v as indented JSON followed by a newline.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
