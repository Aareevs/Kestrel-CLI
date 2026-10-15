package config

import (
	"net/url"
	"regexp"
	"strings"
)

// Mask is the placeholder shown instead of a secret.
const Mask = "********"

var (
	exactRefRe = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)
	// secretAssignRe matches NAME=value pairs whose name looks secret-bearing.
	secretAssignRe = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API_?KEY|PRIVATE_?KEY|CREDENTIALS?)[A-Z0-9_]*)=(\S+)`)
)

// Masked returns a deep copy of c that is safe to display: literal env values
// are masked (bare ${VAR} references are kept), URL passwords are replaced by "xxxxx", and
// secret-looking NAME=value assignments in commands are masked.
// This is best-effort; see docs/security.md.
func (c *Config) Masked() *Config {
	out := *c
	out.Redaction.ExtraPatterns = append([]string(nil), c.Redaction.ExtraPatterns...)
	out.Services = make([]Service, len(c.Services))
	for i, s := range c.Services {
		s.Command = maskCommand(s.Command)
		s.DependsOn = append([]string(nil), s.DependsOn...)
		if s.Env != nil {
			env := make(map[string]string, len(s.Env))
			for k, v := range s.Env {
				if exactRefRe.MatchString(v) {
					env[k] = v
				} else {
					env[k] = Mask
				}
			}
			s.Env = env
		}
		if s.Health != nil {
			h := *s.Health
			h.URL = maskURL(h.URL)
			s.Health = &h
		}
		out.Services[i] = s
	}
	out.Verify.Checks = make([]Check, len(c.Verify.Checks))
	for i, ch := range c.Verify.Checks {
		ch.Run = maskCommand(ch.Run)
		out.Verify.Checks[i] = ch
	}
	return &out
}

func maskCommand(s string) string {
	return secretAssignRe.ReplaceAllString(s, "${1}="+Mask)
}

func maskURL(s string) string {
	if s == "" || !strings.Contains(s, "@") {
		return s
	}
	u, err := url.Parse(s)
	if err != nil || u.User == nil {
		return s
	}
	return u.Redacted()
}
