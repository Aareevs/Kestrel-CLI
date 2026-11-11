package app

import "github.com/Aareevs/Kestrel-CLI/internal/privacy"

// scrubbed passes a report through the project's current redaction rules once
// more before it leaves the app, so every command and the MCP server show
// stored text redacted with today's rules, not only the rules in force when it
// was recorded (a pattern added since then, or a gap fixed, still applies).
// A kestrel.yaml that can't be read must not switch redaction off: the
// built-in rules are used then.
func scrubbed[T any](a *App, v *T, err error) (*T, error) {
	if err != nil || v == nil {
		return v, err
	}
	red, rerr := a.redactor()
	if rerr != nil {
		red = privacy.Default
	}
	out, rerr := privacy.RedactValue(red, v)
	if rerr != nil {
		return nil, rerr
	}
	return out, nil
}
