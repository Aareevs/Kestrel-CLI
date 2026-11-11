// Package termtest is a tiny terminal emulator for tests: it replays the bytes
// a program wrote and lets a test look at the resulting screen. It understands
// only what Kestrel's own output uses and panics on any other escape sequence,
// so a new sequence can't slip through unchecked.
package termtest

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// VT is just enough of a terminal to check what a screen looks like after the
// pane and the log have both written to it: printable text, \r \n, cursor
// moves (H), erase (J, K), scroll regions (r) and cursor save/restore (7, 8).
// Colors (m) are ignored. Scrolled-off lines go to history.
type VT struct {
	rows, cols     int
	screen         [][]rune
	history        []string
	r, c           int
	top, bottom    int // scroll region, 0-based inclusive
	savedR, savedC int
}

// New returns a blank terminal of cols x rows.
func New(cols, rows int) *VT {
	v := &VT{rows: rows, cols: cols, bottom: rows - 1}
	v.screen = make([][]rune, rows)
	for i := range v.screen {
		v.screen[i] = v.blank()
	}
	return v
}

func (v *VT) blank() []rune { return []rune(strings.Repeat(" ", v.cols)) }

func (v *VT) lineFeed() {
	if v.r == v.bottom {
		v.history = append(v.history, strings.TrimRight(string(v.screen[v.top]), " "))
		copy(v.screen[v.top:v.bottom], v.screen[v.top+1:v.bottom+1])
		v.screen[v.bottom] = v.blank()
		return
	}
	if v.r < v.rows-1 {
		v.r++
	}
}

func (v *VT) Write(p []byte) (int, error) {
	s := string(p)
	for i := 0; i < len(s); {
		ch, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case ch == '\n': // a tty in cooked mode turns \n into \r\n (ONLCR)
			v.c = 0
			v.lineFeed()
		case ch == '\r':
			v.c = 0
		case ch == 0x1b && i+1 < len(s) && s[i+1] == '7':
			v.savedR, v.savedC = v.r, v.c
			n = 2
		case ch == 0x1b && i+1 < len(s) && s[i+1] == '8':
			v.r, v.c = v.savedR, v.savedC
			n = 2
		case ch == 0x1b && i+1 < len(s) && s[i+1] == '[':
			j := i + 2
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ';' || s[j] == '?') {
				j++
			}
			params, final := strings.Split(s[i+2:j], ";"), s[j]
			num := func(k, def int) int {
				if k < len(params) {
					if x, err := strconv.Atoi(params[k]); err == nil {
						return x
					}
				}
				return def
			}
			switch final {
			case 'H':
				v.r, v.c = min(num(0, 1)-1, v.rows-1), min(num(1, 1)-1, v.cols-1)
			case 'K':
				if num(0, 0) == 2 {
					v.screen[v.r] = v.blank()
				}
			case 'r':
				if s[i+2:j] == "" {
					v.top, v.bottom = 0, v.rows-1
				} else {
					v.top, v.bottom = num(0, 1)-1, num(1, v.rows)-1
				}
				v.r, v.c = 0, 0
			case 'm', 'h', 'l':
			default:
				panic("vt: unsupported sequence " + strconv.Quote(s[i:j+1]))
			}
			n = j + 1 - i
		default:
			if v.c >= v.cols { // would wrap: a real terminal soft-wraps here
				v.c = 0
				v.lineFeed()
			}
			v.screen[v.r][v.c] = ch
			v.c++
		}
		i += n
	}
	return len(p), nil
}

func (v *VT) Line(i int) string { return strings.TrimRight(string(v.screen[i]), " ") }

func (v *VT) Text() string {
	var out []string
	for i := range v.screen {
		out = append(out, v.Line(i))
	}
	return strings.Join(out, "\n")
}

// History returns the lines that scrolled off the top of the scroll region.
func (v *VT) History() []string { return v.history }

// Region returns the scroll region's first and last rows (0-based).
func (v *VT) Region() (top, bottom int) { return v.top, v.bottom }

// Size returns the terminal's columns and rows.
func (v *VT) Size() (cols, rows int) { return v.cols, v.rows }

// Resize changes the reported size (the screen contents are left as they are).
func (v *VT) Resize(cols, rows int) { v.cols, v.rows = cols, rows }
