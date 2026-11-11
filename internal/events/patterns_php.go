package events

import (
	"regexp"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// PHP and Laravel. A missing vendor/ or class extends P013, PHP's memory
// limit extends P012, and PHPUnit failures extend P014.
//
// The database shape (P033) comes before the generic fatal error (P032),
// because PHP reports a failed PDO connection as `Fatal error: Uncaught
// PDOException: SQLSTATE[…]`.
func init() {
	for _, p := range []Pattern{
		{
			ID:          "P033-php-db-unavailable",
			Description: "PHP (PDO, Laravel) could not reach or log in to its database",
			Type:        api.EventDependencyConnFailed,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`(?:SQLSTATE\[\w+\] \[(?:1045|1049|2002|2006)\]|could not find driver)`),
			Match: []string{
				"SQLSTATE[HY000] [2002] No such file or directory",
				"SQLSTATE[HY000] [1045] Access denied for user 'root'@'localhost' (using password: YES)",
				"SQLSTATE[HY000] [1049] Unknown database 'orders'",
				"PDOException: could not find driver",
			},
			NoMatch:  []string{"SQLSTATE[23000]: Integrity constraint violation: 1062 Duplicate entry", "query ran in 3ms"},
			Priority: 1482,
		},
		{
			ID:          "P032-php-fatal",
			Description: "PHP hit a fatal or parse error",
			Type:        api.EventLogLine,
			Severity:    api.SeverityFatal,
			Re:          regexp.MustCompile(`^(?:PHP )?(?:Fatal|Parse) error:\s`),
			Match: []string{
				"PHP Fatal error:  Uncaught Error: Call to undefined function foo() in /app/index.php:3",
				"Fatal error: Uncaught Exception: boom in /app/x.php:7",
				"PHP Parse error:  syntax error, unexpected '}' in /app/x.php on line 4",
			},
			NoMatch:  []string{"PHP Warning:  Undefined variable $x in /app/x.php on line 2", "handled a fatal error gracefully"},
			Priority: 1484,
		},
	} {
		MustRegister(p)
	}
}
