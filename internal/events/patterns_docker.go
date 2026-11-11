package events

import (
	"regexp"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Docker and Docker Compose. Kestrel supervises `docker compose up` like any
// other command, and a container's own output is classified like a process's
// (the "api-1  | " prefix is ignored: see stripComposePrefix). These patterns
// cover what Docker itself says: the daemon, ports, images, builds, and the
// compose file.
func init() {
	for _, p := range []Pattern{
		{
			ID:          "P034-docker-daemon-unavailable",
			Description: "the Docker daemon is not running or not reachable (or not permitted)",
			Type:        api.EventDependencyConnFailed,
			Severity:    api.SeverityError,
			Re: regexp.MustCompile(`(?i)(?:Cannot connect to the Docker daemon` +
				`|permission denied while trying to connect to the Docker daemon socket` +
				`|error during connect:.*docker_engine` +
				`|Is the docker daemon running\?)`),
			Match: []string{
				"Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
				"permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock",
				`error during connect: this error may indicate that the docker daemon is not running: Get "http://%2F%2F.%2Fpipe%2Fdocker_engine/v1.24/containers/json": open //./pipe/docker_engine: The system cannot find the file specified.`,
			},
			NoMatch:  []string{"Docker daemon started", "docker version 24.0.7"},
			Priority: 1486,
		},
		{
			ID:          "P035-docker-port-allocated",
			Description: "Docker could not publish a container port because the host port is taken",
			Type:        api.EventPortBindFailed,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`Bind for \S*?:(?P<port>\d{2,5}) failed: port is already allocated`),
			Match: []string{
				"Error response from daemon: driver failed programming external connectivity on endpoint db (ab12cd): Bind for 0.0.0.0:5432 failed: port is already allocated",
			},
			NoMatch:  []string{"Bind for 0.0.0.0:5432 succeeded", "port 5432 is allocated to db"},
			Priority: 1488,
		},
		{
			ID:          "P036-docker-image-pull-failed",
			Description: "Docker could not pull an image (missing, private, rate-limited, or the registry is unreachable)",
			Type:        api.EventLogLine,
			Severity:    api.SeverityError,
			Re: regexp.MustCompile(`(?i)(?:pull access denied` +
				`|manifest for \S+ not found` +
				`|manifest unknown` +
				`|toomanyrequests` +
				`|Error response from daemon: (?:Head|Get) "https?://[^"]+": (?:unauthorized|denied))`),
			Match: []string{
				"Error response from daemon: pull access denied for acme/api, repository does not exist or may require 'docker login'",
				"Error response from daemon: manifest for acme/api:2.1 not found: manifest unknown: manifest unknown",
				"toomanyrequests: You have reached your pull rate limit.",
				`Error response from daemon: Head "https://registry.example.com/v2/acme/api/manifests/latest": unauthorized: authentication required`,
			},
			NoMatch:  []string{"Pulling from library/postgres", "Status: Downloaded newer image for postgres:16"},
			Priority: 1490,
		},
		{
			ID:          "P037-docker-build-failed",
			Description: "a Docker image build failed",
			Type:        api.EventLogLine,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`(?:^ERROR \[[^\]]+\]|failed to solve:|returned a non-zero code: \d+|^ERROR: failed to build)`),
			Match: []string{
				"ERROR [build 3/5] RUN npm ci",
				`failed to solve: process "/bin/sh -c npm ci" did not complete successfully: exit code: 1`,
				"The command '/bin/sh -c npm install' returned a non-zero code: 1",
				"ERROR: failed to build: failed to solve: dockerfile parse error",
			},
			NoMatch:  []string{"[build 3/5] RUN npm ci", "Successfully built 4f5a2c1d9b7e"},
			Priority: 1492,
		},
		{
			ID:          "P038-compose-config-invalid",
			Description: "the Compose file is missing or invalid",
			Type:        api.EventLogLine,
			Severity:    api.SeverityError,
			Re: regexp.MustCompile(`(?:no configuration file provided` +
				`|refers to undefined (?:network|volume|service)` +
				`|yaml: line \d+:` +
				`|Additional property \S+ is not allowed` +
				`|service "[^"]+" depends on undefined service)`),
			Match: []string{
				"no configuration file provided: not found",
				`service "api" refers to undefined network backend: invalid compose project`,
				"yaml: line 12: did not find expected key",
				"(root) Additional property servces is not allowed",
				`service "api" depends on undefined service "db": invalid compose project`,
			},
			NoMatch:  []string{"Compose file loaded from docker-compose.yml", "yaml parsed in 3ms"},
			Priority: 1494,
		},
		{
			ID:          "P039-docker-name-conflict",
			Description: "a container with that name already exists",
			Type:        api.EventLogLine,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`The container name "[^"]+" is already in use`),
			Match: []string{
				`Error response from daemon: Conflict. The container name "/db" is already in use by container "9c1f3a". You have to remove (or rename) that container to be able to reuse that name.`,
			},
			NoMatch:  []string{"container name db-1 chosen"},
			Priority: 1496,
		},
		{
			ID:          "P040-compose-dependency-failed",
			Description: "Compose refused to start a service because a container it depends on is unhealthy or exited",
			Type:        api.EventLogLine,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`dependency failed to start: container \S+ (?:is unhealthy|exited \(\d+\))`),
			Match: []string{
				"dependency failed to start: container db-1 is unhealthy",
				"dependency failed to start: container db-1 exited (1)",
			},
			NoMatch:  []string{"dependency db-1 started", "container db-1 is healthy"},
			Priority: 1497,
		},
		{
			ID:          "P041-container-exited",
			Description: "a container exited with a failure code (not 0, and not 143, which is a normal stop)",
			Type:        api.EventLogLine,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`\bexited with code (?:[1-9]\d?|1[0-3]\d|14[0-24-9]|1[5-9]\d|2\d\d)\b`),
			Match: []string{
				"api-1 exited with code 1",
				"worker-1 exited with code 137",
				"db-1 exited with code 255",
			},
			NoMatch:  []string{"api-1 exited with code 0", "api-1 exited with code 143", "exited with code zero"},
			Priority: 1498,
		},
	} {
		MustRegister(p)
	}
}
