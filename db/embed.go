// Package db embeds SQL migrations and development fixtures.
package db

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations is rooted at the migrations directory, as goose expects.
var Migrations, _ = fs.Sub(migrations, "migrations")

//go:embed seed/dev.sql
var DevSeed string
