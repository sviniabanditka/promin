// Package webdist embeds the built web UI bundle. Until the frontend
// build pipeline exists, it only contains a placeholder index.html.
package webdist

import "embed"

//go:embed all:*
var FS embed.FS
