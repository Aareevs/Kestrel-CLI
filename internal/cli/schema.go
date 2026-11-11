package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Aareevs/Kestrel-CLI/docs"
	"github.com/Aareevs/Kestrel-CLI/internal/schemas"
)

func newSchemaCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "schema [diagnose|timeline|verify]",
		Short: "Print the JSON Schema of a report's --json output",
		Long: "Prints the JSON Schema (draft 2020-12) of what `kestrel diagnose --json`, `timeline --json` or\n" +
			"`verify --json` prints, for validating it in scripts and CI or generating types from it. With no\n" +
			"argument, lists the schemas. The same files are in docs/schemas. They don't forbid extra properties,\n" +
			"so a field added in a later release doesn't make an older copy fail.\n\n" +
			"Examples:\n" +
			"  kestrel schema verify > verify.schema.json\n" +
			"  kestrel verify --json | check-jsonschema --schemafile verify.schema.json -",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: schemas.Names(),
		RunE: runE(func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				for _, n := range schemas.Names() {
					title, err := schemas.Title(n)
					if err != nil {
						return err
					}
					fmt.Fprintf(g.streams.Out, "%-9s %s\n", n, g.style.Dim(title))
				}
				fmt.Fprintf(g.streams.Out, "\n%s\n", g.style.Dim("kestrel schema <name> prints one"))
				return nil
			}
			b, err := docs.Schemas.ReadFile("schemas/" + args[0] + ".schema.json")
			if err != nil {
				return &ExitError{Code: ExitUsage, Err: fmt.Errorf("no schema %q", args[0]),
					Next: "use one of: " + strings.Join(schemas.Names(), ", ")}
			}
			_, err = g.streams.Out.Write(b)
			return err
		}),
	}
}
