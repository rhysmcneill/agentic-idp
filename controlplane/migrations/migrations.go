// Package migrations embeds the SQL migration files so they ship inside the
// controlplane binary — no separate migration step or file mount at deploy time.
package migrations

import "embed"

// FS holds the SQL migration files, embedded into the binary.
//
//go:embed *.sql
var FS embed.FS
