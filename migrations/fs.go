package migrations

import "embed"

// FS holds versioned SQL migrations.
//
//go:embed *.sql
var FS embed.FS
