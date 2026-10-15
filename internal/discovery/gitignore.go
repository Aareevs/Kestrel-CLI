package discovery

import (
	"regexp"
	"strings"
)

// gitignore is a pragmatic subset of git's ignore rules: comments, negation,
// directory-only patterns (trailing "/"), anchoring (a "/" anywhere but the
// end), "*", "?", "[...]" and "**". It is used only to skip directories and
// files during discovery, so a rare mismatch costs a few extra reads, never
// correctness elsewhere.
type gitignore struct {
	rules []ignoreRule
}

type ignoreRule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
	// basename is true for unanchored patterns, which match the last path
	// component at any depth.
	basename bool
}

func parseGitignore(content string) *gitignore {
	gi := &gitignore{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasSuffix(line, `\ `) {
			line = strings.TrimRight(line, " \t")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var r ignoreRule
		if strings.HasPrefix(line, "!") {
			r.negate = true
			line = line[1:]
		} else if strings.HasPrefix(line, `\`) {
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly = true
			line = strings.TrimRight(line, "/")
		}
		if line == "" {
			continue
		}
		anchored := strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		r.basename = !anchored
		re, err := regexp.Compile("^" + globToRegexp(line) + "$")
		if err != nil {
			continue
		}
		r.re = re
		gi.rules = append(gi.rules, r)
	}
	return gi
}

// match reports whether rel (relative to the .gitignore's directory) matches,
// and whether the deciding rule was a negation. Later rules win.
func (g *gitignore) match(rel string, isDir bool) (matched, negated bool) {
	name := rel
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		name = rel[i+1:]
	}
	for _, r := range g.rules {
		if r.dirOnly && !isDir {
			continue
		}
		subject := rel
		if r.basename {
			subject = name
		}
		if r.re.MatchString(subject) {
			matched, negated = true, r.negate
		}
	}
	return matched, negated
}

func globToRegexp(glob string) string {
	var b strings.Builder
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				atStart := i == 0 || glob[i-1] == '/'
				i++
				if i+1 < len(glob) && glob[i+1] == '/' && atStart {
					// "**/" matches zero or more directories.
					b.WriteString("(?:.*/)?")
					i++
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			end := strings.IndexByte(glob[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := glob[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			i += end + 1
		case '\\':
			if i+1 < len(glob) {
				i++
				b.WriteString(regexp.QuoteMeta(string(glob[i])))
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}
