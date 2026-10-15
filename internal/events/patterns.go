package events

import (
	"regexp"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Pattern recognizes one well-known error shape in a log line.
//
// Named capture groups become attributes: (?P<port>…), (?P<target>…).
// Every pattern lists lines it must match and lines it must not; the test
// suite enforces both, so adding a pattern means adding its evidence.
type Pattern struct {
	ID          string
	Description string
	Type        api.EventType
	Severity    api.Severity
	Re          *regexp.Regexp
	Match       []string
	NoMatch     []string
}

// Patterns are tried in order; the first match wins, so specific shapes come
// before generic ones.
var Patterns = []Pattern{
	{
		ID:          "P001-port-in-use",
		Description: "the process could not bind its port because something else holds it",
		Type:        api.EventPortBindFailed,
		Severity:    api.SeverityError,
		Re: regexp.MustCompile(`(?i)(?:` +
			`listen (?:tcp\S*\s+)?\S*?:(?P<port>\d{2,5}): bind: address already in use` +
			`|EADDRINUSE:? (?:address already in use )?\S*?:(?P<port2>\d{2,5})` +
			`|port (?P<port3>\d{2,5}) is (?:already )?in use` +
			`|address already in use` +
			`|EADDRINUSE` +
			`|only one usage of each socket address)`),
		Match: []string{
			"listen tcp :8080: bind: address already in use",
			"Error: listen EADDRINUSE: address already in use :::3000",
			"OSError: [Errno 98] Address already in use",
			"Port 5173 is in use, trying another one...",
			"Error starting userland proxy: listen tcp4 0.0.0.0:5432: bind: address already in use",
			"Only one usage of each socket address (protocol/network address/port) is normally permitted.",
		},
		NoMatch: []string{"server listening on :8080", "port 3000 is free"},
	},
	{
		ID:          "P002-bind-permission",
		Description: "the process is not allowed to bind its port (privileged port or sandbox)",
		Type:        api.EventPortBindFailed,
		Severity:    api.SeverityError,
		Re:          regexp.MustCompile(`(?i)(?:listen EACCES:? (?:permission denied )?\S*?:(?P<port>\d{1,5})|listen tcp \S*?:(?P<port2>\d{1,5}): bind: permission denied)`),
		Match: []string{
			"Error: listen EACCES: permission denied 0.0.0.0:80",
			"listen tcp :443: bind: permission denied",
		},
		NoMatch: []string{"open /etc/shadow: permission denied"},
	},
	{
		ID:          "P003-postgres-unavailable",
		Description: "a PostgreSQL client could not reach or start a session with the server",
		Type:        api.EventDependencyConnFailed,
		Severity:    api.SeverityError,
		Re: regexp.MustCompile(`(?i)(?:` +
			`connection to server at "[^"]*" \([^)]*\), port (?P<port>\d+) failed` +
			`|could not connect to server` +
			`|the database system is (?:starting up|shutting down|in recovery mode)` +
			`|pq: .*?(?:connection refused|password authentication failed|database "[^"]+" does not exist)` +
			`|FATAL:\s+(?:password authentication failed|database "[^"]+" does not exist|role "[^"]+" does not exist))`),
		Match: []string{
			`psql: error: connection to server at "localhost" (127.0.0.1), port 5432 failed: Connection refused`,
			"could not connect to server: Connection refused",
			"FATAL:  the database system is starting up",
			`pq: database "orders" does not exist`,
			`FATAL:  password authentication failed for user "app"`,
		},
		NoMatch: []string{"database system is ready to accept connections"},
	},
	{
		ID:          "P004-mysql-unavailable",
		Description: "a MySQL client could not reach the server",
		Type:        api.EventDependencyConnFailed,
		Severity:    api.SeverityError,
		Re:          regexp.MustCompile(`(?i)(?:can't connect to (?:local )?mysql server(?: on '(?P<target>[^']+)')?|ER_ACCESS_DENIED_ERROR|ER_BAD_DB_ERROR)`),
		Match: []string{
			"ERROR 2003 (HY000): Can't connect to MySQL server on 'localhost:3306' (111)",
			"Error: ER_ACCESS_DENIED_ERROR: Access denied for user 'root'@'localhost'",
		},
		NoMatch: []string{"mysqld: ready for connections."},
	},
	{
		ID:          "P005-redis-unavailable",
		Description: "a Redis client could not reach the server",
		Type:        api.EventDependencyConnFailed,
		Severity:    api.SeverityError,
		Re:          regexp.MustCompile(`(?i)(?:could not connect to redis at (?P<target>[^\s:]+:\d+)|redis(?:\.exceptions)?\.ConnectionError|\[ioredis\] Unhandled error event: Error: connect ECONNREFUSED (?P<target2>\S+))`),
		Match: []string{
			"Could not connect to Redis at 127.0.0.1:6379: Connection refused",
			"redis.exceptions.ConnectionError: Error 111 connecting to localhost:6379. Connection refused.",
			"[ioredis] Unhandled error event: Error: connect ECONNREFUSED 127.0.0.1:6379",
		},
		NoMatch: []string{"Ready to accept connections tcp"},
	},
	{
		ID:          "P006-connection-refused",
		Description: "a connection to another service was refused (nothing listening on that port)",
		Type:        api.EventDependencyConnFailed,
		Severity:    api.SeverityError,
		Re: regexp.MustCompile(`(?i)(?:` +
			`dial tcp\S* (?P<target>\S+?:\d+): connect: connection refused` +
			`|ECONNREFUSED (?P<target2>\S+?:\d+)` +
			`|proxying request \S+ to https?://(?P<target3>[^/\s]+)\S* \[ECONNREFUSED\]` +
			`|connect ECONNREFUSED(?: (?P<target4>\S+?:\d+))?` +
			`|connection refused` +
			`|Connection refused)`),
		Match: []string{
			"dial tcp 127.0.0.1:5432: connect: connection refused",
			"Error: connect ECONNREFUSED 127.0.0.1:8080",
			"[HPM] Error occurred while proxying request localhost:5173/api to http://localhost:8080/ [ECONNREFUSED]",
			"requests.exceptions.ConnectionError: HTTPConnectionPool(host='localhost', port=8000): Connection refused",
		},
		NoMatch: []string{"connection established to 127.0.0.1:5432"},
	},
	{
		ID:          "P007-host-not-found",
		Description: "a hostname used to reach a dependency does not resolve",
		Type:        api.EventDependencyConnFailed,
		Severity:    api.SeverityError,
		Re:          regexp.MustCompile(`(?i)(?:getaddrinfo (?:ENOTFOUND|EAI_AGAIN) (?P<target>\S+)|dial tcp: lookup (?P<target2>\S+?)(?: on \S+)?: no such host|Name or service not known|nodename nor servname provided)`),
		Match: []string{
			"Error: getaddrinfo ENOTFOUND db",
			"dial tcp: lookup postgres on 127.0.0.11:53: no such host",
			"socket.gaierror: [Errno -2] Name or service not known",
		},
		NoMatch: []string{"resolved db to 172.18.0.2"},
	},
	{
		ID:          "P008-connection-timeout",
		Description: "a connection to a dependency timed out",
		Type:        api.EventDependencyConnFailed,
		Severity:    api.SeverityError,
		Re:          regexp.MustCompile(`(?i)(?:dial tcp (?P<target>\S+?:\d+): (?:i/o timeout|connect: operation timed out)|connect ETIMEDOUT (?P<target2>\S+?:\d+)|connection timed out)`),
		Match: []string{
			"dial tcp 10.0.0.5:5432: i/o timeout",
			"Error: connect ETIMEDOUT 10.0.0.5:6379",
		},
		NoMatch: []string{"request completed in 30ms (timeout 5s)"},
	},
	{
		ID:          "P012-out-of-memory",
		Description: "the process ran out of memory",
		Type:        api.EventLogLine,
		Severity:    api.SeverityFatal,
		Re:          regexp.MustCompile(`(?:JavaScript heap out of memory|fatal error: runtime: out of memory|^MemoryError\b|java\.lang\.OutOfMemoryError)`),
		Match:       []string{"FATAL ERROR: Reached heap limit Allocation failed - JavaScript heap out of memory", "fatal error: runtime: out of memory", "java.lang.OutOfMemoryError: Java heap space"},
		NoMatch:     []string{"memory usage: 120MB"},
	},
	{
		ID:          "P009-go-panic",
		Description: "a Go program panicked",
		Type:        api.EventLogLine,
		Severity:    api.SeverityFatal,
		Re:          regexp.MustCompile(`^(?:panic: |fatal error: )`),
		Match:       []string{"panic: runtime error: invalid memory address or nil pointer dereference", "fatal error: concurrent map writes"},
		NoMatch:     []string{"recovered from panic: boom", "  panic: indented is a stack line"},
	},
	{
		ID:          "P010-node-uncaught",
		Description: "a Node.js process hit an uncaught exception or unhandled rejection",
		Type:        api.EventLogLine,
		Severity:    api.SeverityFatal,
		Re:          regexp.MustCompile(`(?:^Uncaught \w*Error|UnhandledPromiseRejection|^node:internal/\S+:\d+$|\[UnhandledPromiseRejection)`),
		Match: []string{
			"Uncaught TypeError: Cannot read properties of undefined (reading 'id')",
			"node:internal/process/promises:289",
			"[UnhandledPromiseRejection: This error originated either by throwing inside of an async function]",
		},
		NoMatch: []string{"caught TypeError and retried"},
	},
	{
		ID:          "P011-python-traceback",
		Description: "a Python exception traceback started",
		Type:        api.EventLogLine,
		Severity:    api.SeverityError,
		Re:          regexp.MustCompile(`^Traceback \(most recent call last\):`),
		Match:       []string{"Traceback (most recent call last):"},
		NoMatch:     []string{"  File \"app.py\", line 3, in <module>"},
	},
	{
		ID:          "P013-module-not-found",
		Description: "a dependency or module is missing (not installed or wrong path)",
		Type:        api.EventLogLine,
		Severity:    api.SeverityError,
		Re:          regexp.MustCompile(`(?:Error: Cannot find module|ModuleNotFoundError: No module named|ERR_MODULE_NOT_FOUND|cannot find package|no required module provides package)`),
		Match:       []string{"Error: Cannot find module 'express'", "ModuleNotFoundError: No module named 'django'", "main.go:5:2: no required module provides package github.com/x/y"},
		NoMatch:     []string{"loaded module express"},
	},
	{
		ID:          "P014-test-failed",
		Description: "a test run reported failures",
		Type:        api.EventTestFailed,
		Severity:    api.SeverityError,
		Re:          regexp.MustCompile(`(?:^--- FAIL: |^FAIL\s+\S+|^Tests:\s+\d+ failed|^=+ \d+ failed|^FAILED \S+)`),
		Match:       []string{"--- FAIL: TestCreateOrder (0.01s)", "FAIL\texample.com/orders\t0.123s", "Tests:       2 failed, 10 passed, 12 total", "===== 1 failed, 3 passed in 0.12s =====", "FAILED tests/test_api.py::test_create - AssertionError"},
		NoMatch:     []string{"ok  \texample.com/orders\t0.1s", "PASS"},
	},
	{
		ID:          "L001-listening",
		Description: "the process announced the address it is listening on",
		Type:        api.EventLogLine,
		Severity:    api.SeverityInfo,
		Re: regexp.MustCompile(`(?i)(?:` +
			`(?:listening|running|serving|started server|server started|available|ready)\b[^\n]*?(?:https?://)?(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::\]|\[::1\]|::)?:(?P<port>\d{2,5})\b` +
			`|(?:listening|running|serving) on port (?P<port2>\d{2,5})` +
			`|Local:\s+https?://[^\s:/]+:(?P<port3>\d{2,5})` +
			`|Starting development server at https?://[^\s:/]+:(?P<port4>\d{2,5}))`),
		Match: []string{
			"Listening on :8080",
			"  ➜  Local:   http://localhost:5173/",
			"Uvicorn running on http://127.0.0.1:8000 (Press CTRL+C to quit)",
			"Starting development server at http://127.0.0.1:8000/",
			" * Running on http://127.0.0.1:5000",
			"Server listening on port 3000",
			"ready - started server on 0.0.0.0:3000, url: http://localhost:3000",
		},
		NoMatch: []string{"connecting to localhost:5432", "listen tcp :8080: bind: address already in use"},
	},
}

// Pattern IDs are referenced from diagnosis rules and docs; keep them stable.
var patternByID = func() map[string]*Pattern {
	m := make(map[string]*Pattern, len(Patterns))
	for i := range Patterns {
		m[Patterns[i].ID] = &Patterns[i]
	}
	return m
}()

// PatternByID returns the pattern with id, or nil.
func PatternByID(id string) *Pattern { return patternByID[id] }
