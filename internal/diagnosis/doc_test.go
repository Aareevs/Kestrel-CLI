package diagnosis

import (
	"os"
	"testing"
)

func readDoc(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../docs/diagnosis-rules.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
