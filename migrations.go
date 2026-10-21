package cortex

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations is the embedded SQL migration set rooted at "migrations".
// Pass directly to (*Store).ApplyMigrations.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic("embed sub: " + err.Error())
	}
	return sub
}
