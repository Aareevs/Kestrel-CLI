package events

import (
	"regexp"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Ruby and Rails. Missing gems and `LoadError` extend P013; Ruby's refused
// connections extend P006 and the Rails PG gem's extends P003.
//
// The Rails-specific shapes (P029–P031) come before the generic uncaught
// exception (P028), because a Rails error is also printed as one.
func init() {
	for _, p := range []Pattern{
		{
			ID:          "P029-rails-db-setup",
			Description: "Rails has no database yet, or migrations are pending",
			Type:        api.EventLogLine,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`(?:ActiveRecord::NoDatabaseError|ActiveRecord::PendingMigrationError|Migrations are pending)`),
			Match: []string{
				"ActiveRecord::NoDatabaseError: We could not find your database: orders_development. Which can be found in the database configuration file",
				"ActiveRecord::PendingMigrationError: Migrations are pending. To resolve this issue, run: bin/rails db:migrate",
			},
			NoMatch:  []string{"Migrating to CreateUsers (20260101000000)", "Migrations are up to date"},
			Priority: 1474,
		},
		{
			ID:          "P030-rails-server-running",
			Description: "Rails refused to start because a server PID file exists",
			Type:        api.EventLogLine,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`A server is already running`),
			Match: []string{
				"A server is already running. Check /app/tmp/pids/server.pid.",
				"A server is already running (pid: 4821, file: /app/tmp/pids/server.pid).",
			},
			NoMatch:  []string{"Use Ctrl-C to stop", "Puma starting in single mode"},
			Priority: 1476,
		},
		{
			ID:          "P031-app-secret-missing",
			Description: "the application has no encryption key or secret (Rails credentials, Laravel APP_KEY)",
			Type:        api.EventLogLine,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`(?i)(?:Missing .?secret_key_base|Missing encryption key to decrypt|No application encryption key has been specified)`),
			Match: []string{
				"ArgumentError: Missing `secret_key_base` for 'production' environment, set this string with `bin/rails credentials:edit`",
				"Missing encryption key to decrypt file with. Ask your team for your master key and write it to config/master.key",
				"No application encryption key has been specified.",
			},
			NoMatch:  []string{"encryption key rotated", "secret_key_base loaded"},
			Priority: 1478,
		},
		{
			ID:          "P028-ruby-uncaught",
			Description: "a Ruby program died with an uncaught exception",
			Type:        api.EventLogLine,
			Severity:    api.SeverityFatal,
			Re:          regexp.MustCompile("^\\S+\\.rb:\\d+:in [`'][^']*': .+ \\([A-Z]\\w*(?:::\\w+)*\\)"),
			Match: []string{
				"app.rb:3:in `<main>': undefined method `foo' for nil (NoMethodError)",
				"/app/lib/greeter.rb:12:in 'Greeter#hello': boom (RuntimeError)",
			},
			NoMatch:  []string{"\tfrom /app/lib/greeter.rb:3:in `<main>'", "greeter.rb loaded"},
			Priority: 1480,
		},
	} {
		MustRegister(p)
	}
}
