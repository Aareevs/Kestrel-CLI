// Package collectors turns process output into a bounded, ordered stream of
// redacted, classified events.
package collectors

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// DefaultMaxLine is the longest line kept intact; longer lines are truncated
// with a marker. bufio.Scanner would fail outright on such lines.
const DefaultMaxLine = 16 << 10

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// ReadLines calls fn for every line in r until EOF or a read error. Lines
// longer than max bytes are cut at max and the rest of the line is skipped;
// fn receives the number of bytes dropped. ANSI escape sequences and carriage
// returns are removed and invalid UTF-8 is replaced.
func ReadLines(r io.Reader, max int, fn func(line string, truncated int)) error {
	if max <= 0 {
		max = DefaultMaxLine
	}
	br := bufio.NewReaderSize(r, max)
	for {
		chunk, err := br.ReadSlice('\n')
		switch {
		case err == nil:
			fn(clean(chunk), 0)
		case errors.Is(err, bufio.ErrBufferFull):
			line := clean(chunk)
			skipped, rerr := discardLine(br)
			fn(line, skipped)
			if rerr != nil {
				return eofOK(rerr)
			}
		default:
			if len(chunk) > 0 {
				fn(clean(chunk), 0)
			}
			return eofOK(err)
		}
	}
}

// discardLine skips to the end of the current line and reports how many bytes it skipped.
func discardLine(br *bufio.Reader) (int, error) {
	n := 0
	for {
		chunk, err := br.ReadSlice('\n')
		n += len(chunk)
		if err == nil {
			return n - 1, nil // the newline itself is not content
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return n, err
		}
	}
}

func eofOK(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func clean(b []byte) string {
	b = bytes.TrimRight(b, "\r\n")
	if bytes.IndexByte(b, 0x1b) >= 0 {
		b = ansiRe.ReplaceAll(b, nil)
	}
	s := string(b)
	if strings.IndexByte(s, '\r') >= 0 {
		// Progress bars redraw with \r; keep only the final state.
		s = s[strings.LastIndexByte(s, '\r')+1:]
	}
	s = strings.ToValidUTF8(s, "�")
	// Remaining control characters (bells, stray escapes, backspaces) could
	// still drive the terminal Kestrel prints to; log text is inert data.
	return strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\t') || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}

// TruncationMarker is appended to lines cut by ReadLines.
func TruncationMarker(n int) string {
	return fmt.Sprintf(" …[truncated %d bytes]", n)
}
