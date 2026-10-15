package appdir

import (
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	tests := []struct {
		name, goos, home, want string
		env                    map[string]string
		overridden, wantErr    bool
	}{
		{name: "darwin", goos: "darwin", home: "/Users/a", want: "/Users/a/Library/Application Support/Kestrel"},
		{name: "linux xdg", goos: "linux", home: "/home/a", env: map[string]string{"XDG_DATA_HOME": "/xdg"}, want: "/xdg/kestrel"},
		{name: "linux relative xdg ignored", goos: "linux", home: "/home/a", env: map[string]string{"XDG_DATA_HOME": "rel"}, want: "/home/a/.local/share/kestrel"},
		{name: "linux fallback", goos: "linux", home: "/home/a", want: "/home/a/.local/share/kestrel"},
		{name: "windows", goos: "windows", env: map[string]string{"LOCALAPPDATA": "/lad"}, want: "/lad/Kestrel"},
		{name: "windows missing", goos: "windows", wantErr: true},
		{name: "override", goos: "darwin", home: "/Users/a", env: map[string]string{"KESTREL_HOME": "/custom"}, want: "/custom", overridden: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, over, err := resolve(tt.goos, env(tt.env), tt.home)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			want := filepath.FromSlash(tt.want)
			if tt.name == "override" {
				want, _ = filepath.Abs(tt.env["KESTREL_HOME"])
			}
			if got != want || over != tt.overridden {
				t.Fatalf("got %q (overridden=%v), want %q (%v)", got, over, want, tt.overridden)
			}
		})
	}
}
