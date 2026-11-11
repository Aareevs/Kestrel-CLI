package app

import (
	"slices"
	"testing"
)

func TestChildEnvUnbuffersPython(t *testing.T) {
	has := func(env []string, kv string) bool { return slices.Contains(env, kv) }
	count := func(env []string, key string) (n int) {
		for _, kv := range env {
			if len(kv) > len(key) && kv[:len(key)+1] == key+"=" {
				n++
			}
		}
		return
	}

	// By default it is switched on, and the rest of the environment is kept.
	env := childEnv([]string{"PATH=/bin", "HOME=/h"}, nil)
	if !has(env, "PYTHONUNBUFFERED=1") || !has(env, "PATH=/bin") || !has(env, "HOME=/h") {
		t.Errorf("default: %v", env)
	}
	// The user's own choice wins, from the parent environment or the service's env, even when empty.
	if env = childEnv([]string{"PYTHONUNBUFFERED=0"}, nil); !has(env, "PYTHONUNBUFFERED=0") || count(env, "PYTHONUNBUFFERED") != 1 {
		t.Errorf("parent value: %v", env)
	}
	if env = childEnv([]string{"PYTHONUNBUFFERED=1"}, map[string]string{"PYTHONUNBUFFERED": ""}); !has(env, "PYTHONUNBUFFERED=") || count(env, "PYTHONUNBUFFERED") != 1 {
		t.Errorf("service value: %v", env)
	}
	// Service variables still override the parent's, and the caller's map isn't touched.
	extra := map[string]string{"PORT": "9000"}
	if env = childEnv([]string{"PORT=1", "A=b"}, extra); !has(env, "PORT=9000") || has(env, "PORT=1") || !has(env, "PYTHONUNBUFFERED=1") {
		t.Errorf("override: %v", env)
	}
	if len(extra) != 1 {
		t.Errorf("the caller's map was modified: %v", extra)
	}
}
