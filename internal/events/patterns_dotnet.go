package events

import (
	"regexp"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// .NET (ASP.NET Core, Kestrel server, Npgsql, SqlClient, MSBuild).
//
// P025 runs before every built-in: Kestrel words a taken port as "Failed to
// bind to address http://…:5000: address already in use", which P001 would
// claim without the port.
func init() {
	for _, p := range []Pattern{
		{
			ID:          "P025-dotnet-bind-failed",
			Description: "ASP.NET Core's Kestrel server could not bind its address (the port is taken)",
			Type:        api.EventPortBindFailed,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`Failed to bind to address \S*?:(?P<port>\d{2,5})\b`),
			Match: []string{
				"Unable to start Kestrel. System.IO.IOException: Failed to bind to address http://127.0.0.1:5000: address already in use.",
				"Failed to bind to address http://[::]:5001: Only one usage of each socket address (protocol/network address/port) is normally permitted.",
			},
			NoMatch:  []string{"Now listening on: http://localhost:5000", "Bound to address http://localhost:5000"},
			Priority: 50,
		},
		{
			ID:          "P026-dotnet-db-unavailable",
			Description: "a .NET database client (Npgsql, SqlClient) could not reach its server",
			Type:        api.EventDependencyConnFailed,
			Severity:    api.SeverityError,
			Re: regexp.MustCompile(`(?i)(?:Failed to connect to (?P<target>[^\s:]+:\d+)` +
				`|A network-related or instance-specific error occurred while establishing a connection to SQL Server)`),
			Match: []string{
				"Npgsql.NpgsqlException (0x80004005): Failed to connect to 127.0.0.1:5432",
				"Microsoft.Data.SqlClient.SqlException (0x80131904): A network-related or instance-specific error occurred while establishing a connection to SQL Server.",
			},
			NoMatch:  []string{"Connected to 127.0.0.1:5432", "Failed to connect: retrying"},
			Priority: 1466,
		},
		{
			ID:          "P024-dotnet-unhandled",
			Description: "a .NET process died with an unhandled exception",
			Type:        api.EventLogLine,
			Severity:    api.SeverityFatal,
			Re:          regexp.MustCompile(`^Unhandled [Ee]xception[.:] `),
			Match: []string{
				"Unhandled exception. System.InvalidOperationException: Sequence contains no elements",
				"Unhandled Exception: System.NullReferenceException: Object reference not set to an instance of an object.",
			},
			NoMatch:  []string{"Handled exception, retrying", "info: unhandled requests are logged"},
			Priority: 1470,
		},
		{
			ID:          "P027-dotnet-build-failed",
			Description: "dotnet build or MSBuild failed",
			Type:        api.EventLogLine,
			Severity:    api.SeverityError,
			Re:          regexp.MustCompile(`(?:^Build FAILED\.|\berror (?:CS|NETSDK|MSB|NU)\d{4}:)`),
			Match: []string{
				"Build FAILED.",
				"Program.cs(12,5): error CS1002: ; expected [/app/demo.csproj]",
				"/usr/share/dotnet/sdk/8.0.100/Sdks/Microsoft.NET.Sdk/targets/Microsoft.NET.TargetFrameworkInference.targets(144,5): error NETSDK1045: The current .NET SDK does not support targeting .NET 9.0.",
			},
			NoMatch:  []string{"Build succeeded.", "    0 Error(s)"},
			Priority: 1472,
		},
	} {
		MustRegister(p)
	}
}
