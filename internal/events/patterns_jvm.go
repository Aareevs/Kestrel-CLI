package events

import (
	"regexp"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Java and Kotlin (JVM) patterns: Spring Boot, JDBC, Maven and Gradle.
//
// They sit after the built-in generic shapes (OOM, missing modules, test
// failures: P012–P014 also cover JVM lines) and before the listen announcement.
// Among themselves the specific shapes come first, because JVM errors often
// arrive wrapped as `Exception in thread "main" …`, which P015 would
// otherwise claim.
func init() {
	for _, p := range []Pattern{
		{
			ID:          "P016-spring-port-in-use",
			Description: "Spring Boot could not start its web server because the port is taken",
			Type:        api.EventPortBindFailed,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`Port (?P<port>\d{2,5}) was already in use`),
			Match:       []string{"Web server failed to start. Port 8080 was already in use."},
			NoMatch:     []string{"Tomcat initialized with port 8080 (http)", "Port 8080 is free"},
			Priority:    1440,
		},
		{
			ID:          "P017-spring-startup-failed",
			Description: "Spring Boot's application context failed to start (bean wiring, configuration, or a startup failure report)",
			Type:        api.EventLogLine,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`(?:APPLICATION FAILED TO START|Application run failed|BeanCreationException|UnsatisfiedDependencyException|Error creating bean with name)`),
			Match: []string{
				"***************************\nAPPLICATION FAILED TO START",
				"2026-01-02 10:00:00.000 ERROR 1 --- [           main] o.s.boot.SpringApplication               : Application run failed",
				"org.springframework.beans.factory.UnsatisfiedDependencyException: Error creating bean with name 'userController'",
			},
			NoMatch:  []string{"Started DemoApplication in 3.2 seconds (process running for 3.8)", "Creating bean for user service"},
			Priority: 1442,
		},
		{
			ID:          "P018-jdbc-unavailable",
			Description: "a JDBC client could not reach or open a connection to its database",
			Type:        api.EventDependencyConnFailed,
			Severity:    api.SeverityError,
			Re: regexp.MustCompile(`(?i)(?:connection to (?P<target>[^\s:]+:\d+) refused` +
				`|communications link failure` +
				`|CannotGetJdbcConnectionException` +
				`|HikariPool-\d+ - Exception during pool initialization)`),
			Match: []string{
				"org.postgresql.util.PSQLException: Connection to localhost:5432 refused. Check that the hostname and port are correct and that the postmaster is accepting TCP/IP connections.",
				"com.mysql.cj.jdbc.exceptions.CommunicationsException: Communications link failure",
				"HikariPool-1 - Exception during pool initialization.",
			},
			NoMatch:  []string{"HikariPool-1 - Start completed.", "Connection to localhost:5432 established"},
			Priority: 1444,
		},
		{
			ID:          "P019-jvm-class-version",
			Description: "the JVM is older than the one the code was compiled for",
			Type:        api.EventLogLine,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`(?:UnsupportedClassVersionError|compiled by a more recent version of the Java Runtime)`),
			Match: []string{
				`Exception in thread "main" java.lang.UnsupportedClassVersionError: com/acme/App has been compiled by a more recent version of the Java Runtime (class file version 65.0), this version of the Java Runtime only recognizes class file versions up to 61.0`,
			},
			NoMatch:  []string{"running on Java Runtime 17.0.9"},
			Priority: 1446,
		},
		{
			ID:          "P020-jvm-build-failed",
			Description: "a Maven or Gradle build failed",
			Type:        api.EventLogLine,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`^(?:BUILD FAILED\b|FAILURE: Build failed with an exception|Execution failed for task\b|\[ERROR\] (?:COMPILATION ERROR|Failed to execute goal))`),
			Match: []string{
				"BUILD FAILED in 4s",
				"FAILURE: Build failed with an exception.",
				"Execution failed for task ':compileJava'.",
				"[ERROR] COMPILATION ERROR :",
				"[ERROR] Failed to execute goal org.apache.maven.plugins:maven-compiler-plugin:3.11.0:compile (default-compile) on project demo",
			},
			NoMatch:  []string{"BUILD SUCCESSFUL in 3s", "[INFO] BUILD SUCCESS"},
			Priority: 1448,
		},
		{
			ID:          "P015-jvm-main-uncaught",
			Description: "the JVM's main thread died with an uncaught exception",
			Type:        api.EventLogLine,
			Severity:    api.SeverityFatal,
			Re:          regexp.MustCompile(`^Exception in thread "main" \S*(?:Exception|Error)\b`),
			Match: []string{
				`Exception in thread "main" java.lang.IllegalStateException: Failed to start the server`,
				`Exception in thread "main" kotlin.NotImplementedError: An operation is not implemented`,
			},
			NoMatch: []string{
				`Exception in thread "worker-1" java.lang.RuntimeException: retrying`,
				"caught java.lang.IllegalStateException and retried",
			},
			Priority: 1450,
		},
	} {
		MustRegister(p)
	}
}
