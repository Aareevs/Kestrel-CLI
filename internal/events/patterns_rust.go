package events

import (
	"regexp"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Rust: panics and cargo build failures. A taken port (`AddrInUse`) and a
// refused connection are already recognized by P001 and P006.
func init() {
	for _, p := range []Pattern{
		{
			ID:          "P021-rust-main-panic",
			Description: "a Rust program's main thread panicked",
			Type:        api.EventLogLine,
			Severity:    api.SeverityFatal,
			Re:          regexp.MustCompile(`^thread 'main' panicked at`),
			Match: []string{
				"thread 'main' panicked at src/main.rs:12:5:",
				"thread 'main' panicked at 'called `Option::unwrap()` on a `None` value', src/main.rs:4:10",
			},
			NoMatch:  []string{"thread 'worker-1' panicked at src/lib.rs:1:1:", "the main thread continued"},
			Priority: 1460,
		},
		{
			ID:          "P022-rust-thread-panic",
			Description: "a thread of a Rust program panicked (the process may keep running)",
			Type:        api.EventLogLine,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`^thread '[^']+' panicked at`),
			Match:       []string{"thread 'tokio-runtime-worker' panicked at src/handler.rs:30:9:"},
			NoMatch:     []string{"thread pool started with 4 workers"},
			Priority:    1462,
		},
		{
			ID:          "P023-cargo-build-failed",
			Description: "cargo could not compile the project",
			Type:        api.EventLogLine,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`^(?:error: (?:could not compile|aborting due to|linking with)|error\[E\d{4}\]:)`),
			Match: []string{
				"error: could not compile `demo` (bin \"demo\") due to 1 previous error",
				"error[E0425]: cannot find value `x` in this scope",
				"error: linking with `cc` failed: exit status: 1",
			},
			NoMatch:  []string{"warning: unused variable: `x`", "   Compiling demo v0.1.0 (/app)"},
			Priority: 1464,
		},
	} {
		MustRegister(p)
	}
}
