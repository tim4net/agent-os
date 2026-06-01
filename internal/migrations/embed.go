// Package migrations embeds all SQL migration files so the binary is self-contained.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
