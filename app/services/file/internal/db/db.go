// Package db embeds the SQL applied by internal.Migrate(): init scripts run
// first (one transaction), then versioned migrations (sql-migrate), then the
// views and procedures re-created on every run (one transaction).
package db

import "embed"

//go:embed scripts
var Scripts embed.FS

//go:embed migrations
var Migrations embed.FS

//go:embed view
var Views embed.FS

//go:embed procedure
var Procedures embed.FS
