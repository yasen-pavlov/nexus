// Package migrations embeds SQL migration files for use with goose.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
