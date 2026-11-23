package internal

import (
	"embed"
	"fmt"

	"platform/postgres"
)

//go:embed migration/*.sql
var MigrationFS embed.FS

// RunMigrations automatically runs goose migrations for the auth service.
func RunMigrations(dbURL string) error {
	if err := postgres.Migrate(dbURL, MigrationFS, "migration"); err != nil {
		return fmt.Errorf("auth migration failed: %w", err)
	}
	return nil
}
