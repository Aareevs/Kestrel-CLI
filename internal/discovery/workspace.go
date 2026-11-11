package discovery

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Workspaces
//
// A monorepo declares its members in one file: pnpm-workspace.yaml, the
// `workspaces` field of package.json (npm, yarn, bun), go.work, or the
// [workspace] table of Cargo.toml. Discovery reads that declaration, because it
// is stronger evidence than a directory happening to hold a manifest:
//
//   - members deeper than the depth limit are read too (the walk can't see them);
//   - a root script that only runs the members (`turbo run dev`) isn't listed
//     as a service of its own, since the members are listed individually;
//   - declared members that don't exist are reported.
//
// Everything here follows the scanner's rules: only reads, never follows
// symlinks, skips vendored directories and .gitignore'd ones, bounded work.

const (
	// maxWorkspaceDirs bounds how many directories expanding member patterns
	// may look at, so a pattern like "**" can't walk a huge tree.
	maxWorkspaceDirs = 2000
	// maxWorkspaceDepth bounds how deep (from the project root) a member may be.
	maxWorkspaceDepth = 8
)

// workspaceDef is one workspace declaration, before its members are expanded.
type workspaceDef struct {
	kind     string
	root     string // slash-relative directory, "." for the project root
	file     string // slash-relative path of the declaring file
	patterns []string
	excludes []string
}

// discoverWorkspaces finds every workspace declaration among the scanned
// directories and reads the members that lie beyond the walk's depth.
func (s *scanner) discoverWorkspaces() {
	for _, dir := range s.sortedDirs() {
		for _, def := range s.workspaceDefs(dir) {
			members, missing := s.expandMembers(def)
			for _, m := range missing {
				s.warnf("%s: workspace member %q does not exist", def.file, m)
			}
			for _, m := range members {
				s.readMember(m)
			}
			s.workspaces = append(s.workspaces, workspace{def: def, members: members})
		}
	}
}

// workspace is a declaration with its expanded members.
type workspace struct {
	def     workspaceDef
	members []string
}

// workspaceDefs reads the workspace declarations in dir. A pnpm workspace file
// takes precedence over a `workspaces` field in the same directory.
func (s *scanner) workspaceDefs(dir string) []workspaceDef {
	var out []workspaceDef
	pnpm := false
	if rel := joinRel(dir, "pnpm-workspace.yaml"); s.has(rel) {
		if raw, ok := s.readSeen(rel); ok {
			var doc struct {
				Packages []string `yaml:"packages"`
			}
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				s.warnf("could not parse %s: %v", rel, err)
			} else if len(doc.Packages) > 0 {
				d := workspaceDef{kind: "pnpm", root: dir, file: rel}
				for _, p := range doc.Packages {
					if strings.HasPrefix(p, "!") {
						d.excludes = append(d.excludes, strings.TrimPrefix(p, "!"))
					} else {
						d.patterns = append(d.patterns, p)
					}
				}
				out, pnpm = append(out, d), true
			}
		}
	}
	if rel := joinRel(dir, "package.json"); s.has(rel) && !pnpm {
		if raw, ok := s.readSeen(rel); ok {
			if pats := packageWorkspaces(raw); len(pats) > 0 {
				d := workspaceDef{kind: "npm", root: dir, file: rel}
				if pm := s.nodePackageManager(dir, packageJSON{}); pm != "npm" && pm != "pnpm" {
					d.kind = pm // yarn or bun, from the lockfile
				}
				for _, p := range pats {
					if strings.HasPrefix(p, "!") {
						d.excludes = append(d.excludes, strings.TrimPrefix(p, "!"))
					} else {
						d.patterns = append(d.patterns, p)
					}
				}
				out = append(out, d)
			}
		}
	}
	if rel := joinRel(dir, "go.work"); s.has(rel) {
		if raw, ok := s.readSeen(rel); ok {
			if uses := goWorkUses(raw); len(uses) > 0 {
				out = append(out, workspaceDef{kind: "go", root: dir, file: rel, patterns: uses})
			}
		}
	}
	if rel := joinRel(dir, "Cargo.toml"); s.has(rel) {
		if raw, ok := s.readSeen(rel); ok {
			if members, excludes := cargoWorkspace(raw); len(members) > 0 {
				out = append(out, workspaceDef{kind: "cargo", root: dir, file: rel, patterns: members, excludes: excludes})
			}
		}
	}
	return out
}

// packageWorkspaces returns package.json's `workspaces`: either an array of
// patterns or an object with a `packages` array (yarn).
func packageWorkspaces(raw []byte) []string {
	var doc struct {
		Workspaces json.RawMessage `json:"workspaces"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.Workspaces) == 0 {
		return nil
	}
	var list []string
	if json.Unmarshal(doc.Workspaces, &list) == nil {
		return list
	}
	var obj struct {
		Packages []string `json:"packages"`
	}
	if json.Unmarshal(doc.Workspaces, &obj) == nil {
		return obj.Packages
	}
	return nil
}

var (
	goWorkUseRe   = regexp.MustCompile(`(?m)^\s*use\s+([^\s(][^\n]*)$`)
	goWorkBlockRe = regexp.MustCompile(`(?ms)^\s*use\s*\((.*?)^\s*\)`)
	tomlStringRe  = regexp.MustCompile(`"([^"]*)"|'([^']*)'`)
	tomlArrayRe   = func(key string) *regexp.Regexp {
		return regexp.MustCompile(`(?ms)^\s*` + key + `\s*=\s*\[(.*?)\]`)
	}
	cargoSectionRe = regexp.MustCompile(`(?m)^\s*\[([^\]]+)\]\s*$`)
)

// goWorkUses returns the directories of go.work's `use` directives, in both
// the single-line and the block form.
func goWorkUses(raw []byte) []string {
	var out []string
	clean := func(line string) string {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		return strings.Trim(strings.TrimSpace(line), `"`)
	}
	for _, m := range goWorkBlockRe.FindAllSubmatch(raw, -1) {
		for _, line := range strings.Split(string(m[1]), "\n") {
			if d := clean(line); d != "" {
				out = append(out, d)
			}
		}
	}
	for _, m := range goWorkUseRe.FindAllSubmatch(raw, -1) {
		if d := clean(string(m[1])); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// cargoWorkspace returns the members and excludes of a Cargo.toml's
// [workspace] table.
func cargoWorkspace(raw []byte) (members, excludes []string) {
	idx := cargoSectionRe.FindAllSubmatchIndex(raw, -1)
	for i, m := range idx {
		if strings.TrimSpace(string(raw[m[2]:m[3]])) != "workspace" {
			continue
		}
		end := len(raw)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		body := raw[m[1]:end]
		pick := func(key string) []string {
			arr := tomlArrayRe(key).FindSubmatch(body)
			if arr == nil {
				return nil
			}
			var out []string
			for _, sm := range tomlStringRe.FindAllSubmatch(stripTomlComments(arr[1]), -1) {
				v := string(sm[1])
				if v == "" {
					v = string(sm[2])
				}
				out = append(out, v)
			}
			return out
		}
		return pick("members"), pick("exclude")
	}
	return nil, nil
}

// stripTomlComments removes # comments outside strings (one line at a time).
func stripTomlComments(b []byte) []byte {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		in, esc, cut := byte(0), false, len(line)
		for i := 0; i < len(line); i++ {
			ch := line[i]
			switch {
			case in != 0:
				if esc {
					esc = false
				} else if ch == '\\' && in == '"' {
					esc = true
				} else if ch == in {
					in = 0
				}
			case ch == '"' || ch == '\'':
				in = ch
			case ch == '#':
				cut = i
			}
			if cut != len(line) {
				break
			}
		}
		out = append(out, line[:cut])
	}
	return []byte(strings.Join(out, "\n"))
}

// expandMembers turns a declaration's patterns into member directories (sorted,
// slash-relative to the project root). Literal patterns that name a directory
// that doesn't exist are returned separately so they can be reported.
func (s *scanner) expandMembers(def workspaceDef) (members, missing []string) {
	budget := maxWorkspaceDirs
	set := map[string]bool{}
	for _, pat := range def.patterns {
		found := s.expandPattern(def.root, pat, &budget)
		if len(found) == 0 && !hasGlob(pat) {
			missing = append(missing, pat)
		}
		for _, m := range found {
			set[m] = true
		}
	}
	for _, pat := range def.excludes {
		for _, m := range s.expandPattern(def.root, pat, &budget) {
			delete(set, m)
		}
	}
	for m := range set {
		if m != def.root {
			members = append(members, m)
		}
	}
	sort.Strings(members)
	sort.Strings(missing)
	if budget <= 0 {
		s.warnf("%s: workspace member patterns matched too many directories; the list may be incomplete", def.file)
	}
	return members, missing
}

func hasGlob(p string) bool { return strings.ContainsAny(p, "*?[") }

// expandPattern resolves one pattern (segments of literals, `*`, `?`, `[...]`
// and `**`) against the real directories below root.
func (s *scanner) expandPattern(root, pattern string, budget *int) []string {
	pattern = strings.TrimPrefix(path.Clean(filepath.ToSlash(pattern)), "./")
	if pattern == "." || pattern == "" || pattern == ".." || strings.HasPrefix(pattern, "../") || strings.HasPrefix(pattern, "/") {
		return nil // never leave the project
	}
	var out []string
	var walk func(base string, segs []string)
	walk = func(base string, segs []string) {
		if *budget <= 0 {
			return
		}
		if len(segs) == 0 {
			out = append(out, base)
			return
		}
		seg, rest := segs[0], segs[1:]
		switch {
		case seg == "**":
			walk(base, rest)
			if strings.Count(base, "/")+1 >= maxWorkspaceDepth {
				return
			}
			for _, child := range s.childDirs(base, budget) {
				walk(joinRel(base, child), segs)
			}
		case hasGlob(seg):
			for _, child := range s.childDirs(base, budget) {
				if ok, _ := path.Match(seg, child); ok {
					walk(joinRel(base, child), rest)
				}
			}
		default:
			next := joinRel(base, seg)
			if s.usableDir(next, true) {
				walk(next, rest)
			}
		}
	}
	walk(root, strings.Split(pattern, "/"))
	return dedupe(out)
}

// childDirs lists the subdirectories of rel that discovery may look at.
func (s *scanner) childDirs(rel string, budget *int) []string {
	entries, err := os.ReadDir(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() { // symlinks to directories report as non-dirs here: never followed
			continue
		}
		*budget--
		if *budget <= 0 {
			break
		}
		if s.usableDir(joinRel(rel, e.Name()), false) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// usableDir reports whether rel is a real directory inside the project that
// the scanner may read: not a symlink, not in a skipped or ignored directory.
// A directory a pattern merely matches (`packages/*`) is never a vendored or
// build-output directory (node_modules, dist, …). One a workspace names
// explicitly (`tools/build`) may be, but nothing inside such an ancestor is.
func (s *scanner) usableDir(rel string, explicit bool) bool {
	if rel == "." {
		return true
	}
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		last := i == len(parts)-1
		if skipDirs[p] && (!last || !explicit) {
			return false
		}
	}
	// Ancestors' .gitignore files (not necessarily walked) apply to rel.
	for i := 1; i < len(parts); i++ {
		anc := strings.Join(parts[:i], "/")
		if s.ignores[anc] == nil {
			s.loadGitignore(anc)
		}
	}
	if s.ignored(rel, true) {
		return false
	}
	fi, err := os.Lstat(filepath.Join(s.root, filepath.FromSlash(rel)))
	return err == nil && fi.IsDir() && fi.Mode()&os.ModeSymlink == 0
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// readMember makes sure a member directory and its own tree (to the usual
// depth, counted from the member) have been scanned.
func (s *scanner) readMember(rel string) {
	if strings.Count(rel, "/")+1 > maxWorkspaceDepth {
		return
	}
	// Even a member inside the depth limit is walked again from itself: its own
	// subdirectories (cmd/<name>, src) may lie beyond the limit counted from the root.
	if err := s.walkFrom(rel); err != nil {
		s.warnf("could not read workspace member %s: %v", rel, unwrapPathErr(err))
	}
}

// ---- after analysis -----------------------------------------------------

// orchestratorRe matches a script that only runs other packages' scripts.
var orchestratorRe = regexp.MustCompile(`\b(?:turbo|nx|lerna|rush|concurrently|npm-run-all|run-p|run-s|wsrun)\b` +
	`|\bpnpm\s+(?:-r\b|--recursive\b|--filter\b|-F\b|--parallel\b)` +
	`|\byarn\s+workspaces\s+foreach\b` +
	`|(?:\s--workspaces\b|\s-ws\b)`)

// applyWorkspaces reports the workspaces and drops a root candidate whose
// script only orchestrates the members' own scripts.
func (s *scanner) applyWorkspaces(cands []*candidate) ([]*candidate, []api.Workspace) {
	if len(s.workspaces) == 0 {
		return cands, nil
	}
	var out []api.Workspace
	drop := map[*candidate]bool{}
	for _, ws := range s.workspaces {
		w := api.Workspace{Kind: ws.def.kind, Root: ws.def.root, File: ws.def.file, Members: []string{}, Services: []string{}}
		isMember := map[string]bool{}
		for _, m := range ws.members {
			w.Members = append(w.Members, m)
			isMember[m] = true
		}
		var rootCand *candidate
		for _, c := range cands {
			switch {
			case isMember[c.dir]:
				w.Services = append(w.Services, c.dir)
			case c.dir == ws.def.root && c.origin == originNative && c.Technology == "node" && ws.def.kind != "go" && ws.def.kind != "cargo":
				rootCand = c
			}
		}
		w.Services = dedupe(w.Services)
		sort.Strings(w.Services)
		if rootCand != nil && len(w.Services) > 0 && orchestratorRe.MatchString(rootCand.script) {
			w.Orchestrator = rootCand.Command.Value + ": " + rootCand.script
			drop[rootCand] = true
		}
		out = append(out, w)
	}
	if len(drop) == 0 {
		return cands, out
	}
	kept := cands[:0:0]
	for _, c := range cands {
		if !drop[c] {
			kept = append(kept, c)
		}
	}
	return kept, out
}
