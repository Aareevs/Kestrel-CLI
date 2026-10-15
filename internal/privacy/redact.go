// Package privacy redacts secrets from text before Kestrel prints, stores,
// exports, or sends it anywhere. Redaction is pattern-based and best-effort.
package privacy

import (
	"fmt"
	"regexp"
	"strings"
)

// rule replaces matches of re. If keep > 0, the first keep capture groups are
// preserved and only the rest of the match is replaced (e.g. keep the
// "password=" key, replace its value).
type rule struct {
	kind string
	re   *regexp.Regexp
	keep int
}

func placeholder(kind string) string { return "[REDACTED:" + kind + "]" }

// builtin rules, most specific first.
var builtin = []rule{
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`), 0},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{4,}\.eyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}`), 0},
	{"url-credentials", regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^\s:/@]+:)[^\s@/]+(@)`), 0},
	{"anthropic-key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`), 0},
	{"openai-key", regexp.MustCompile(`\bsk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{20,}`), 0},
	{"stripe-key", regexp.MustCompile(`\b(?:sk|rk|pk)_(?:live|test)_[A-Za-z0-9]{16,}`), 0},
	{"github-token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})`), 0},
	{"slack-token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`), 0},
	{"aws-access-key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), 0},
	{"google-api-key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`), 0},
	{"bearer", regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`), 1},
	{"basic-auth", regexp.MustCompile(`(?i)\b(basic\s+)[A-Za-z0-9+/]{8,}={0,2}`), 1},
	// Header lines: "Authorization: …", "Cookie: …", "Set-Cookie: …", "X-Api-Key: …".
	{"header", regexp.MustCompile(`(?i)\b((?:authorization|proxy-authorization|cookie|set-cookie|x-api-key|x-auth-token)\s*[:=]\s*)[^\r\n]+`), 1},
	// key=value / key: value / "key": "value" pairs with secret-sounding keys.
	{"secret", regexp.MustCompile(`(?i)((?:^|[^A-Za-z0-9])["']?[A-Za-z0-9_.-]*(?:password|passwd|pwd|secret[_-]?key|secret|token|api[_-]?key|apikey|access[_-]?key|private[_-]?key|client[_-]?secret|credentials?)["']?\s*[:=]\s*["']?)([^\s"',;&}]+)`), 1},
}

// Redactor removes secrets from strings. It is safe for concurrent use.
type Redactor struct {
	rules []rule
}

// New returns a Redactor with the built-in rules plus extra patterns (from
// redaction.extra_patterns), whose whole matches are replaced.
func New(extra []string) (*Redactor, error) {
	r := &Redactor{rules: append([]rule(nil), builtin...)}
	for i, p := range extra {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("redaction.extra_patterns[%d]: %w", i, err)
		}
		r.rules = append(r.rules, rule{kind: "custom", re: re})
	}
	return r, nil
}

// Default is a Redactor with only the built-in rules.
var Default, _ = New(nil)

// Redact returns s with every recognized secret replaced by a placeholder.
func (r *Redactor) Redact(s string) string {
	if s == "" {
		return s
	}
	for _, ru := range r.rules {
		if !ru.re.MatchString(s) {
			continue
		}
		ph := placeholder(ru.kind)
		switch {
		case ru.kind == "url-credentials":
			s = ru.re.ReplaceAllString(s, "${1}"+ph+"${2}")
		case ru.keep > 0:
			s = ru.re.ReplaceAllStringFunc(s, func(m string) string {
				sub := ru.re.FindStringSubmatch(m)
				if sub == nil || len(sub) <= ru.keep {
					return ph
				}
				prefix := strings.Join(sub[1:ru.keep+1], "")
				if strings.HasPrefix(sub[len(sub)-1], "[REDACTED") {
					return m // already redacted by a more specific rule
				}
				return prefix + ph
			})
		default:
			s = ru.re.ReplaceAllString(s, ph)
		}
	}
	return s
}

// Stream redacts one line-oriented stream. It remembers whether it is inside
// a multi-line private key block so the body lines are redacted too. Use one
// Stream per output stream; it is not safe for concurrent use.
type Stream struct {
	r     *Redactor
	inKey bool
}

// NewStream returns a line redactor backed by r.
func (r *Redactor) NewStream() *Stream { return &Stream{r: r} }

var (
	keyBeginRe = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)
	keyEndRe   = regexp.MustCompile(`-----END [A-Z0-9 ]*PRIVATE KEY-----`)
)

// Line redacts a single line.
func (s *Stream) Line(line string) string {
	if s.inKey {
		if loc := keyEndRe.FindStringIndex(line); loc != nil {
			s.inKey = false
			return placeholder("private-key") + s.r.Redact(line[loc[1]:])
		}
		return placeholder("private-key")
	}
	if loc := keyBeginRe.FindStringIndex(line); loc != nil && !keyEndRe.MatchString(line[loc[1]:]) {
		s.inKey = true
		return s.r.Redact(line[:loc[0]]) + placeholder("private-key")
	}
	return s.r.Redact(line)
}
