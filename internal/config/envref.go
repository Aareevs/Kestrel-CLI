package config

import (
	"fmt"
	"strings"
)

// Refs returns the names of ${VAR} references in s, in order of appearance.
// A lone "$" is literal; "${" must be closed and name a valid variable.
func Refs(s string) ([]string, error) {
	var names []string
	for i := 0; i < len(s); i++ {
		if s[i] != '$' || i+1 >= len(s) || s[i+1] != '{' {
			continue
		}
		end := strings.IndexByte(s[i+2:], '}')
		if end < 0 {
			return nil, fmt.Errorf("unclosed \"${\" at position %d", i)
		}
		name := s[i+2 : i+2+end]
		if !envKeyRe.MatchString(name) {
			return nil, fmt.Errorf("invalid variable reference \"${%s}\"", name)
		}
		names = append(names, name)
		i += 2 + end
	}
	return names, nil
}

// Expand replaces ${VAR} references using lookup. Every missing variable is
// reported by name; resolved values never appear in errors.
func Expand(s string, lookup func(string) (string, bool)) (string, error) {
	if _, err := Refs(s); err != nil {
		return "", err
	}
	var b strings.Builder
	var missing []string
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '{' {
			end := strings.IndexByte(s[i+2:], '}')
			name := s[i+2 : i+2+end]
			if val, ok := lookup(name); ok {
				b.WriteString(val)
			} else {
				missing = append(missing, name)
			}
			i += 2 + end
			continue
		}
		b.WriteByte(s[i])
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("environment variable(s) not set: %s", strings.Join(missing, ", "))
	}
	return b.String(), nil
}

// ExpandEnv resolves every service's env references, returning per-service
// environments. The config itself is not modified, so resolved values are
// never shown by `config show`.
func (c *Config) ExpandEnv(lookup func(string) (string, bool)) (map[string]map[string]string, error) {
	out := make(map[string]map[string]string, len(c.Services))
	for _, s := range c.Services {
		env := make(map[string]string, len(s.Env))
		for k, v := range s.Env {
			val, err := Expand(v, lookup)
			if err != nil {
				return nil, fmt.Errorf("service %s, env %s: %w", s.Name, k, err)
			}
			env[k] = val
		}
		out[s.Name] = env
	}
	return out, nil
}
