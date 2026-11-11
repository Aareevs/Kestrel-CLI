# JSON Schemas for report output

| Schema | Describes | Also |
|---|---|---|
| [`diagnose.schema.json`](diagnose.schema.json) | `kestrel diagnose --json` | the MCP tool `get_diagnosis` |
| [`timeline.schema.json`](timeline.schema.json) | `kestrel timeline --json` | the MCP tool `get_failure_timeline` |
| [`verify.schema.json`](verify.schema.json) | `kestrel verify --json` | |

JSON Schema draft 2020-12. `kestrel schema <name>` prints the same file from an installed binary. (The config
file has its own: [`../kestrel.schema.json`](../kestrel.schema.json).)

## What they promise

- Every field Kestrel prints is listed, with its type, whether it is always present (`required`), its doc comment
  as `description`, and, for closed sets (verdicts, claim levels, severities, event types, check statuses, …),
  the allowed values as `enum`.
- Timestamps are strings with `format: date-time`. Arrays are always arrays (`[]` when empty, never `null`), and
  an absent optional field is omitted rather than `null`.
- `schema_version` is `1`. Fields are **added** within a version; changing or removing one bumps it.
- They **don't** set `additionalProperties: false`, so a report from a newer release (with a field your copy
  doesn't know) still validates. Validate strictly only if you pin the Kestrel version.

## How they stay true

They are generated from the Go types in `pkg/api`, not written by hand: descriptions come from the types' doc
comments and enums from their `const` blocks. Two tests keep them honest:

- `TestCommittedSchemasAreCurrent` fails if a type changes and the files weren't regenerated
  (`make schemas`, which is `go test ./internal/schemas -update`).
- `validateSchema` in the integration tests validates what the real binary prints (every incident scenario,
  every verify test, `-I`/`--service`/`--since` variants) against the committed files, so a schema can't drift
  from reality (a field that is `null` where the schema says array, a value missing from an enum).

## Using them

```bash
kestrel verify --json | check-jsonschema --schemafile <(kestrel schema verify) -
kestrel diagnose --json | jq -e '.incidents | length'     # or generate types with your favourite tool
```
