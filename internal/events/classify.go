package events

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Classification is what Kestrel can say about a single log line.
type Classification struct {
	Type      api.EventType
	Severity  api.Severity
	PatternID string
	Port      int
	Target    string
	// Explicit is true when the line itself stated its level.
	Explicit bool
}

var (
	levelKVRe   = regexp.MustCompile(`(?i)(?:^|[\s{,])"?(?:level|lvl|severity)"?\s*[=:]\s*"?(debug|trace|info|notice|warn|warning|error|err|fatal|panic|critical|crit)\b`)
	levelNumRe  = regexp.MustCompile(`"level"\s*:\s*(10|20|30|40|50|60)\b`) // pino/bunyan
	levelTokRe  = regexp.MustCompile(`(?:^|[\s\[(|])(DEBUG|TRACE|INFO|NOTICE|WARN|WARNING|ERROR|ERR|FATAL|PANIC|CRITICAL|CRIT)(?:[\]):|\s]|$)`)
	levelHeadRe = regexp.MustCompile(`^(?i:(error|warning|warn|fatal))(?:\[[^\]]*\])?:\s`)
)

func parseLevel(s string) api.Severity {
	switch strings.ToLower(s) {
	case "debug", "trace", "10", "20":
		return api.SeverityDebug
	case "info", "notice", "30":
		return api.SeverityInfo
	case "warn", "warning", "40":
		return api.SeverityWarn
	case "error", "err", "50":
		return api.SeverityError
	case "fatal", "panic", "critical", "crit", "60":
		return api.SeverityFatal
	}
	return ""
}

// Level returns the severity a line declares about itself, if any. Only the
// first 80 bytes are searched for bare tokens so that message text such as
// "retrying after ERROR from upstream" deep in a line is not mistaken for the
// line's own level.
func Level(line string) (api.Severity, bool) {
	if m := levelKVRe.FindStringSubmatch(line); m != nil {
		return parseLevel(m[1]), true
	}
	if m := levelNumRe.FindStringSubmatch(line); m != nil {
		return parseLevel(m[1]), true
	}
	if m := levelHeadRe.FindStringSubmatch(line); m != nil {
		return parseLevel(m[1]), true
	}
	head := line
	if len(head) > 80 {
		head = head[:80]
	}
	if m := levelTokRe.FindStringSubmatch(head); m != nil {
		return parseLevel(m[1]), true
	}
	return "", false
}

// Classify assigns a type, severity and attributes to one log line.
//
// The line's own declared level wins over a pattern's default severity
// (a "WARN … connection refused, retrying" stays a warning), except that a
// pattern with fatal severity (panic, OOM) always raises it.
func Classify(line string) Classification {
	c := Classification{Type: api.EventLogLine, Severity: api.SeverityInfo}
	if lvl, ok := Level(line); ok {
		c.Severity, c.Explicit = lvl, true
	}
	for i := range Patterns {
		p := &Patterns[i]
		m := p.Re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		c.Type = p.Type
		c.PatternID = p.ID
		if !c.Explicit || p.Severity.Rank() > c.Severity.Rank() && p.Severity == api.SeverityFatal {
			c.Severity = p.Severity
		}
		for gi, name := range p.Re.SubexpNames() {
			if gi == 0 || m[gi] == "" {
				continue
			}
			switch {
			case strings.HasPrefix(name, "port") && c.Port == 0:
				c.Port, _ = strconv.Atoi(m[gi])
			case strings.HasPrefix(name, "target") && c.Target == "":
				c.Target = strings.TrimRight(m[gi], ".,;:)]")
			}
		}
		if c.Port == 0 && c.Target != "" {
			if i := strings.LastIndexByte(c.Target, ':'); i >= 0 {
				c.Port, _ = strconv.Atoi(c.Target[i+1:])
			}
		}
		break
	}
	return c
}
