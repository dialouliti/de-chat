// Package migrations embeds the SQL migration files so they ship inside the
// binary and are applied at startup.
package migrations

import "embed"

// FS holds all *.sql migration files. Migrations must follow the
// golang-migrate naming convention: <version>_<name>.up.sql / .down.sql.
//
//go:embed *.sql
var FS embed.FS
