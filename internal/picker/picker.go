// Package picker is a small arrow-key list selector for terminals.
//
// It is split so the interesting parts need no terminal: keys are decoded from
// a byte stream (decodeKey), the list's cursor and scrolling are a plain model
// (model), and a frame is a pure function of the model (render). Pick glues
// them to a reader and writer; the CLI supplies raw mode, tests supply bytes.
package picker

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/Aareevs/Kestrel-CLI/internal/output"
)

// Item is one row: a short label and a dimmer detail.
type Item struct {
	Label  string
	Detail string
}

// Style is the part of output.Style the picker uses.
type Style interface {
	Bold(string) string
	Dim(string) string
	Cyan(string) string
}

// Options configure Pick.
type Options struct {
	Title   string
	Items   []Item
	Initial int // index selected at the start
	// Height is the most rows shown at once (0: 12). A longer list scrolls.
	Height int
	// Width is the terminal's columns (0: unknown, nothing is shortened).
	Width int
	Style Style
	// Raw puts the terminal in raw mode and returns a function that restores
	// it. Nil means the input is already a stream of keys (tests, pipes).
	Raw func() (restore func(), err error)
}

// ErrCancelled is returned when the user backs out (q, Esc, Ctrl-C).
var ErrCancelled = errors.New("selection cancelled")

type key int

const (
	keyNone key = iota
	keyUp
	keyDown
	keyPageUp
	keyPageDown
	keyHome
	keyEnd
	keyEnter
	keyCancel
	keyDigit // see decoded.digit
)

type decoded struct {
	k     key
	digit int
}

// decodeKey reads one key from r. A lone ESC (nothing buffered behind it) is a
// cancel; ESC followed by [ or O starts an arrow/page/home/end sequence.
func decodeKey(r *bufio.Reader) (decoded, error) {
	b, err := r.ReadByte()
	if err != nil {
		return decoded{}, err
	}
	switch b {
	case '\r', '\n':
		return decoded{k: keyEnter}, nil
	case 0x03, 0x04, 'q', 'Q': // Ctrl-C, Ctrl-D, q
		return decoded{k: keyCancel}, nil
	case 'k', 'K':
		return decoded{k: keyUp}, nil
	case 'j', 'J':
		return decoded{k: keyDown}, nil
	case 'g':
		return decoded{k: keyHome}, nil
	case 'G':
		return decoded{k: keyEnd}, nil
	case 0x1b:
		if r.Buffered() == 0 {
			return decoded{k: keyCancel}, nil
		}
		next, _ := r.ReadByte()
		if next != '[' && next != 'O' {
			return decoded{k: keyCancel}, nil
		}
		c, _ := r.ReadByte()
		switch c {
		case 'A':
			return decoded{k: keyUp}, nil
		case 'B':
			return decoded{k: keyDown}, nil
		case 'H':
			return decoded{k: keyHome}, nil
		case 'F':
			return decoded{k: keyEnd}, nil
		case '5', '6', '1', '4', '7', '8':
			end, _ := r.ReadByte() // the trailing ~
			_ = end
			switch c {
			case '5':
				return decoded{k: keyPageUp}, nil
			case '6':
				return decoded{k: keyPageDown}, nil
			case '1', '7':
				return decoded{k: keyHome}, nil
			default:
				return decoded{k: keyEnd}, nil
			}
		}
		return decoded{k: keyNone}, nil
	}
	if b >= '1' && b <= '9' {
		return decoded{k: keyDigit, digit: int(b - '0')}, nil
	}
	return decoded{k: keyNone}, nil
}

// model is the list's cursor and the first visible row.
type model struct {
	n, height int
	cursor    int
	top       int
}

func newModel(n, height, initial int) *model {
	m := &model{n: n, height: max(1, min(height, n))}
	m.set(initial)
	return m
}

func (m *model) set(i int) {
	m.cursor = max(0, min(i, m.n-1))
	// Keep the cursor inside the window.
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+m.height {
		m.top = m.cursor - m.height + 1
	}
}

func (m *model) apply(d decoded) {
	switch d.k {
	case keyUp:
		m.set(m.cursor - 1)
	case keyDown:
		m.set(m.cursor + 1)
	case keyPageUp:
		m.set(m.cursor - m.height)
	case keyPageDown:
		m.set(m.cursor + m.height)
	case keyHome:
		m.set(0)
	case keyEnd:
		m.set(m.n - 1)
	case keyDigit:
		m.set(d.digit - 1)
	}
}

// render returns the lines of one frame.
func render(m *model, o Options) []string {
	st := o.Style
	var lines []string
	if o.Title != "" {
		lines = append(lines, st.Bold(o.Title))
	}
	labelW := 0
	for _, it := range o.Items {
		labelW = max(labelW, utf8.RuneCountInString(it.Label))
	}
	numW := len(fmt.Sprint(len(o.Items)))
	if m.top > 0 {
		lines = append(lines, st.Dim(fmt.Sprintf("  ↑ %d more", m.top)))
	}
	for i := m.top; i < m.top+m.height; i++ {
		it := o.Items[i]
		marker, label := "  ", it.Label
		if i == m.cursor {
			marker = st.Cyan("❯ ")
			label = st.Bold(label)
		}
		pad := strings.Repeat(" ", labelW-utf8.RuneCountInString(it.Label))
		num := fmt.Sprintf("%*d", numW, i+1)
		fixed := 2 + numW + 2 + labelW + 2
		detail := it.Detail
		if o.Width > 0 {
			detail = output.Fit(detail, max(o.Width-fixed, 8))
		}
		lines = append(lines, fmt.Sprintf("%s%s  %s%s  %s", marker, st.Dim(num), label, pad, st.Dim(detail)))
	}
	if rest := m.n - m.top - m.height; rest > 0 {
		lines = append(lines, st.Dim(fmt.Sprintf("  ↓ %d more", rest)))
	}
	lines = append(lines, st.Dim("  ↑↓ move · enter select · q cancel"))
	return lines
}

// Pick shows the list and returns the chosen index, or ErrCancelled. The frame
// is redrawn in place and removed when the choice is made.
func Pick(in io.Reader, out io.Writer, o Options) (int, error) {
	if len(o.Items) == 0 {
		return 0, errors.New("nothing to pick from")
	}
	height := o.Height
	if height <= 0 {
		height = 12
	}
	if o.Raw != nil {
		restore, err := o.Raw()
		if err != nil {
			return 0, err
		}
		defer restore()
	}
	m := newModel(len(o.Items), height, o.Initial)
	// A *bufio.Reader is reused as is, so keys typed ahead during one picker
	// are still there for the next.
	r, ok := in.(*bufio.Reader)
	if !ok {
		r = bufio.NewReader(in)
	}

	fmt.Fprint(out, "\x1b[?25l") // hide the cursor while picking
	defer fmt.Fprint(out, "\x1b[?25h")
	drawn := 0
	draw := func() {
		if drawn > 0 {
			fmt.Fprintf(out, "\x1b[%dF\x1b[J", drawn) // back to the frame's first line, clear below
		}
		lines := render(m, o)
		for _, l := range lines {
			fmt.Fprint(out, "\r"+l+"\n")
		}
		drawn = len(lines)
	}
	clear := func() {
		if drawn > 0 {
			fmt.Fprintf(out, "\x1b[%dF\x1b[J", drawn)
			drawn = 0
		}
	}
	draw()
	for {
		d, err := decodeKey(r)
		if err != nil {
			clear()
			if errors.Is(err, io.EOF) {
				return 0, ErrCancelled
			}
			return 0, err
		}
		switch d.k {
		case keyEnter:
			clear()
			return m.cursor, nil
		case keyCancel:
			clear()
			return 0, ErrCancelled
		case keyNone:
			continue
		}
		m.apply(d)
		draw()
	}
}
