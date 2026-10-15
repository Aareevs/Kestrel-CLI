package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxFileSize bounds how much of a config file Kestrel will read.
const MaxFileSize = 1 << 20

// Issue is a single, actionable configuration problem.
type Issue struct {
	Line    int
	Field   string
	Message string
}

func (i Issue) String() string {
	var b strings.Builder
	if i.Line > 0 {
		fmt.Fprintf(&b, "line %d: ", i.Line)
	}
	if i.Field != "" {
		b.WriteString(i.Field)
		b.WriteString(": ")
	}
	b.WriteString(i.Message)
	return b.String()
}

// ValidationError reports every problem found in a config file.
type ValidationError struct {
	Path   string
	Issues []Issue
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d problem(s)", e.Path, len(e.Issues))
	for _, is := range e.Issues {
		b.WriteString("\n  ")
		b.WriteString(is.String())
	}
	return b.String()
}

// ErrAmbiguous is returned when a directory has both kestrel.yaml and .kestrel.yaml.
var ErrAmbiguous = errors.New("both kestrel.yaml and .kestrel.yaml exist; keep only one")

// Find returns the config file in dir, or "" if there is none.
func Find(dir string) (string, error) {
	var found []string
	for _, name := range FileNames {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("%s: %w", dir, ErrAmbiguous)
	}
}

// FindProjectRoot walks up from start looking for a directory containing a
// config file. If none is found it returns start itself and found=false.
func FindProjectRoot(start string) (root string, found bool) {
	dir := start
	for {
		for _, name := range FileNames {
			if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && fi.Mode().IsRegular() {
				return dir, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return start, false
		}
		dir = parent
	}
}

// Load reads, parses, validates, and defaults the config at path. projectRoot
// is used to check service directories; pass "" to skip filesystem checks.
// Problems in the file are returned as *ValidationError.
func Load(path, projectRoot string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileSize {
		return nil, &ValidationError{Path: path, Issues: []Issue{{Message: fmt.Sprintf("file is larger than %d bytes", MaxFileSize)}}}
	}
	return Parse(path, data, projectRoot)
}

// Parse parses and validates config bytes. path is used only for messages.
func Parse(path string, data []byte, projectRoot string) (*Config, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, &ValidationError{Path: path, Issues: yamlIssues(err)}
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		return nil, &ValidationError{Path: path, Issues: []Issue{{Message: "file is empty (start with \"version: 1\")"}}}
	}
	if root.Content[0].Kind != yaml.MappingNode {
		return nil, &ValidationError{Path: path, Issues: []Issue{{Line: root.Content[0].Line, Message: "top level must be a mapping (key: value pairs)"}}}
	}

	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, &ValidationError{Path: path, Issues: yamlIssues(err)}
	}

	issues := validate(&cfg, root.Content[0], projectRoot)
	if len(issues) > 0 {
		return nil, &ValidationError{Path: path, Issues: issues}
	}
	cfg.ApplyDefaults()
	return &cfg, nil
}

var (
	lineRe     = regexp.MustCompile(`^(?:yaml: )?line (\d+): (.*)$`)
	notFoundRe = regexp.MustCompile(`^field (\S+) not found in type config\.(\w+)$`)
)

// yamlIssues turns yaml.v3 errors into readable issues.
func yamlIssues(err error) []Issue {
	var msgs []string
	var te *yaml.TypeError
	if errors.As(err, &te) {
		msgs = te.Errors
	} else {
		msgs = []string{err.Error()}
	}
	issues := make([]Issue, 0, len(msgs))
	for _, m := range msgs {
		is := Issue{Message: m}
		if sm := lineRe.FindStringSubmatch(m); sm != nil {
			is.Line, _ = strconv.Atoi(sm[1])
			is.Message = sm[2]
		}
		if sm := notFoundRe.FindStringSubmatch(is.Message); sm != nil {
			is.Message = fmt.Sprintf("unknown field %q", sm[1])
			if fields := knownFields(sm[2]); len(fields) > 0 {
				is.Message += " (valid here: " + strings.Join(fields, ", ") + ")"
			}
		}
		is.Message = strings.ReplaceAll(is.Message, "config.", "")
		issues = append(issues, is)
	}
	return issues
}

var schemaTypes = map[string]reflect.Type{
	"Config":      reflect.TypeOf(Config{}),
	"Project":     reflect.TypeOf(Project{}),
	"Storage":     reflect.TypeOf(Storage{}),
	"Correlation": reflect.TypeOf(Correlation{}),
	"Redaction":   reflect.TypeOf(Redaction{}),
	"Service":     reflect.TypeOf(Service{}),
	"Health":      reflect.TypeOf(Health{}),
	"Verify":      reflect.TypeOf(Verify{}),
	"Check":       reflect.TypeOf(Check{}),
}

// knownFields lists the YAML keys accepted by a schema type.
func knownFields(typeName string) []string {
	t, ok := schemaTypes[typeName]
	if !ok {
		return nil
	}
	return YAMLFields(t)
}

// YAMLFields returns the sorted yaml keys of a struct type.
func YAMLFields(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		tag := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]
		if tag != "" && tag != "-" {
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out
}
