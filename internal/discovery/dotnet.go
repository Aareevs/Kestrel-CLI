package discovery

import (
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func init() { registerAnalyzer("dotnet", 90, (*scanner).analyzeDotnet) }

var (
	csprojSdkRe     = regexp.MustCompile(`<Project\s+Sdk="([^"]+)"`)
	csprojOutputRe  = regexp.MustCompile(`<OutputType>\s*Exe\s*</OutputType>`)
	csprojAsmRe     = regexp.MustCompile(`<AssemblyName>\s*([^<\s]+)\s*</AssemblyName>`)
	csprojPackageRe = regexp.MustCompile(`<PackageReference\s+Include="([^"]+)"`)
	httpURLRe       = regexp.MustCompile(`http://[^:;"\s]+:(\d{2,5})`)
)

// dotnetLibs maps NuGet package names (lowercase) to an infrastructure kind.
var dotnetLibs = libMap{
	"npgsql": "postgres", "npgsql.entityframeworkcore.postgresql": "postgres",
	"mysql.data": "mysql", "mysqlconnector": "mysql", "pomelo.entityframeworkcore.mysql": "mysql",
	"stackexchange.redis": "redis", "microsoft.extensions.caching.stackexchangeredis": "redis",
	"mongodb.driver": "mongodb", "rabbitmq.client": "rabbitmq", "masstransit.rabbitmq": "rabbitmq",
	"confluent.kafka": "kafka",
}

// analyzeDotnet reads the project files (.csproj, .fsproj, .vbproj) in a
// directory. A web SDK or an Exe output makes a service; a library doesn't.
func (s *scanner) analyzeDotnet(dir string) []*candidate {
	var projects []string
	for _, n := range s.files[dir] {
		switch strings.ToLower(path.Ext(n)) {
		case ".csproj", ".fsproj", ".vbproj":
			projects = append(projects, n)
		}
	}
	sort.Strings(projects)
	var out []*candidate
	for _, proj := range projects {
		if c := s.dotnetProject(dir, proj, len(projects) > 1); c != nil {
			out = append(out, c)
		}
	}
	return out
}

func (s *scanner) dotnetProject(dir, proj string, several bool) *candidate {
	file := joinRel(dir, proj)
	raw, ok := s.readSeen(file)
	if !ok {
		return nil
	}
	sdk := ""
	if m := csprojSdkRe.FindSubmatch(raw); m != nil {
		sdk = string(m[1])
	}
	web := strings.HasSuffix(sdk, ".Web")
	worker := strings.HasSuffix(sdk, ".Worker")
	if !web && !worker && !csprojOutputRe.Match(raw) {
		s.extraTech = append(s.extraTech, "dotnet") // a library project
		return nil
	}

	name := strings.TrimSuffix(proj, path.Ext(proj))
	if m := csprojAsmRe.FindSubmatch(raw); m != nil {
		name = string(m[1])
	}
	name = cleanName(name)
	if name == "" {
		name = s.dirName(dir)
	}
	c := newCandidate(dir, name, "dotnet", originNative)
	c.addSource(api.Source{File: file})
	c.PackageManager = "nuget"
	cmd := "dotnet run"
	if several {
		cmd += " --project " + proj
	}
	note := "console or worker project"
	switch {
	case web:
		c.Framework, c.httpish, note = "aspnet", true, "web SDK ("+sdk+")"
	case worker:
		c.Framework, note = "worker", "worker SDK ("+sdk+")"
	}
	c.setCommand(cmd, api.ConfidenceHigh, api.Source{File: file, Line: lineOf(raw, "<Project"), Note: note})

	if web {
		s.dotnetPort(c, dir)
	}
	for _, m := range csprojPackageRe.FindAllSubmatchIndex(raw, -1) {
		pkg := string(raw[m[2]:m[3]])
		if kind, ok := dotnetLibs.lookup(pkg); ok {
			c.needKind(kind, api.Source{File: file, Line: lineAtOffset(raw, m[0]), Note: "references " + pkg})
		}
	}
	return c
}

// dotnetPort finds the HTTP port: launchSettings.json (what `dotnet run`
// uses), then the Kestrel endpoints in appsettings.json, then the default.
func (s *scanner) dotnetPort(c *candidate, dir string) {
	if rel := joinRel(dir, "Properties/launchSettings.json"); s.has(rel) {
		if raw, ok := s.readSeen(rel); ok {
			var ls struct {
				Profiles map[string]struct {
					URL string            `json:"applicationUrl"`
					Env map[string]string `json:"environmentVariables"`
				} `json:"profiles"`
			}
			if json.Unmarshal(stripJSONC(raw), &ls) == nil {
				profiles := make([]string, 0, len(ls.Profiles))
				for p := range ls.Profiles {
					profiles = append(profiles, p)
				}
				sort.Strings(profiles)
				for _, p := range profiles {
					urls := ls.Profiles[p].URL
					if urls == "" {
						urls = ls.Profiles[p].Env["ASPNETCORE_URLS"]
					}
					if m := httpURLRe.FindStringSubmatch(urls); m != nil {
						if port, err := strconv.Atoi(m[1]); err == nil {
							c.offerPort(port, api.ConfidenceHigh, api.Source{File: rel, Line: lineOf(raw, m[0]), Note: "launch profile \"" + p + "\""})
							return
						}
					}
				}
			}
		}
	}
	for _, cfg := range []string{"appsettings.json", "appsettings.Development.json"} {
		rel := joinRel(dir, cfg)
		if raw, ok := s.readSeen(rel); ok {
			if m := httpURLRe.FindSubmatchIndex(raw); m != nil {
				if p, err := strconv.Atoi(string(raw[m[2]:m[3]])); err == nil {
					c.offerPort(p, api.ConfidenceHigh, api.Source{File: rel, Line: lineAtOffset(raw, m[0]), Note: "Kestrel endpoint"})
					return
				}
			}
		}
	}
	c.offerPort(5000, api.ConfidenceLow, api.Source{File: c.Sources[0].File, Note: "Kestrel default port for `dotnet run`"})
}
