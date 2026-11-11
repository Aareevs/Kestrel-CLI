// Package docs holds the repository's documentation. It exists so the binary
// can carry the published JSON Schemas (docs/schemas) and print them with
// `kestrel schema`.
package docs

import "embed"

// Schemas holds docs/schemas/*.schema.json.
//
//go:embed schemas/*.schema.json
var Schemas embed.FS
