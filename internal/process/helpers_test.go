package process

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// readPIDs reads PIDs a test script or fakesvc wrote, one per line.
func readPIDs(t *testing.T, path string, want int) []int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(path)
		var pids []int
		for _, f := range strings.Fields(string(b)) {
			if n, err := strconv.Atoi(f); err == nil {
				pids = append(pids, n)
			}
		}
		if len(pids) >= want {
			return pids
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d pids written", len(pids), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertAllGone(t *testing.T, pids []int) {
	t.Helper()
	for _, pid := range pids {
		if Alive(pid) {
			t.Errorf("pid %d is still running (orphan)", pid)
		}
	}
}
