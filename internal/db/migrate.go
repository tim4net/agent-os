package db

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tim4net/agent-os/internal/migrations"
)

// MigrationFile represents a single parsed migration file.
type MigrationFile struct {
	Version int64
	Name    string // e.g. "000014_work_events.up.sql"
	Content string
}

// migrationRe matches migration filenames like 000014_work_events.up.sql or 000008_workflows.down.sql.
var migrationRe = regexp.MustCompile(`^(\d+)_([^.]+)\.(up|down)\.sql$`)

// parseMigrations reads embedded SQL files and returns sorted migration files.
// Gaps in version numbers are allowed (e.g. versions 2-7 may not exist).
func parseMigrations() ([]MigrationFile, error) {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	var files []MigrationFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		ver, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			continue
		}

		data, err := migrations.FS.ReadFile(e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		files = append(files, MigrationFile{
			Version: ver,
			Name:    e.Name(),
			Content: string(data),
		})
	}

	// Sort by version, then up before down (deterministic order).
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].Version != files[j].Version {
			return files[i].Version < files[j].Version
		}
		return strings.HasSuffix(files[i].Name, ".up.sql")
	})

	return files, nil
}

// ensureSchemaMigrationsTable creates the schema_migrations tracking table
// if it does not exist. Compatible with golang-migrate format.
func ensureSchemaMigrationsTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version bigint PRIMARY KEY,
			dirty   boolean NOT NULL DEFAULT false
		)
	`)
	if err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}
	return nil
}

// appliedVersions reads the set of successfully applied (non-dirty) migration versions.
func appliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[int64]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations WHERE dirty = false`)
	if err != nil {
		return nil, fmt.Errorf("query applied versions: %w", err)
	}
	defer rows.Close()

	versions := make(map[int64]bool)
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan version: %w", err)
		}
		versions[v] = true
	}
	return versions, rows.Err()
}

// dirtyVersions reads the set of dirty migration versions.
func dirtyVersions(ctx context.Context, pool *pgxpool.Pool) (map[int64]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations WHERE dirty = true`)
	if err != nil {
		return nil, fmt.Errorf("query dirty versions: %w", err)
	}
	defer rows.Close()

	versions := make(map[int64]bool)
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan dirty version: %w", err)
		}
		versions[v] = true
	}
	return versions, rows.Err()
}

// MigrateResult holds the outcome of a MigrateUp call.
type MigrateResult struct {
	Applied int      // number of migrations applied this run
	Versions []int64 // versions that were applied
}

// MigrateUp applies all pending up-migrations to the database.
// Each migration runs in its own transaction. On failure, the transaction
// rolls back and the version is recorded as dirty=true (aborting further
// migrations). Gaps in version numbers are handled naturally — only
// embedded files that exist are applied.
//
// This function is safe to call at server boot or via a CLI subcommand.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool) (*MigrateResult, error) {
	return MigrateUpWithLogger(ctx, pool, nil)
}

// MigrateUpWithLogger is like MigrateUp but accepts an optional logger.
func MigrateUpWithLogger(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (*MigrateResult, error) {
	log := logger
	if log == nil {
		log = slog.Default()
	}

	if err := ensureSchemaMigrationsTable(ctx, pool); err != nil {
		return nil, err
	}

	// Check for existing dirty state.
	dirty, err := dirtyVersions(ctx, pool)
	if err != nil {
		return nil, err
	}
	if len(dirty) > 0 {
		dirtyList := make([]string, 0, len(dirty))
		for v := range dirty {
			dirtyList = append(dirtyList, strconv.FormatInt(v, 10))
		}
		sort.Strings(dirtyList)
		return nil, fmt.Errorf("database has dirty migration(s) [%s] — fix manually before continuing", strings.Join(dirtyList, ", "))
	}

	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return nil, err
	}

	allFiles, err := parseMigrations()
	if err != nil {
		return nil, err
	}

	// Filter to up-migrations that haven't been applied yet.
	var pending []MigrationFile
	for _, f := range allFiles {
		if !strings.HasSuffix(f.Name, ".up.sql") {
			continue
		}
		if applied[f.Version] {
			continue
		}
		pending = append(pending, f)
	}

	result := &MigrateResult{}

	for _, f := range pending {
		log.Info("applying migration", "version", f.Version, "file", f.Name)

		// Begin a transaction for this migration.
		tx, err := pool.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("begin tx for migration %d: %w", f.Version, err)
		}

		// Execute the migration SQL.
		_, err = tx.Exec(ctx, f.Content)
		if err != nil {
			// Mark dirty before rolling back.
			tx.Exec(ctx, `INSERT INTO schema_migrations(version, dirty) VALUES($1, true) ON CONFLICT (version) DO UPDATE SET dirty = true`, f.Version)
			tx.Rollback(ctx)
			return nil, fmt.Errorf("migration %d (%s) failed: %w (marked dirty — fix manually)", f.Version, f.Name, err)
		}

		// Record the version as clean.
		_, err = tx.Exec(ctx,
			`INSERT INTO schema_migrations(version, dirty) VALUES($1, false) ON CONFLICT (version) DO UPDATE SET dirty = false`,
			f.Version,
		)
		if err != nil {
			tx.Rollback(ctx)
			return nil, fmt.Errorf("record migration %d: %w", f.Version, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit migration %d: %w", f.Version, err)
		}

		result.Applied++
		result.Versions = append(result.Versions, f.Version)
		log.Info("migration applied", "version", f.Version, "file", f.Name)
	}

	return result, nil
}

// MigrateDown rolls back a single migration (the highest applied version).
// This is ONLY for explicit operator use — never called automatically.
// The caller must provide the exact version to roll back.
func MigrateDown(ctx context.Context, pool *pgxpool.Pool, version int64, downSQL string) error {
	// Read the down migration content if not provided.
	if downSQL == "" {
		allFiles, err := parseMigrations()
		if err != nil {
			return err
		}
		for _, f := range allFiles {
			if f.Version == version && strings.HasSuffix(f.Name, ".down.sql") {
				downSQL = f.Content
				break
			}
		}
		if downSQL == "" {
			return fmt.Errorf("no down migration found for version %d", version)
		}
	}

	// Check that the version is currently applied and not dirty.
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1 AND dirty = false)`,
		version,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check version %d: %w", version, err)
	}
	if !exists {
		return fmt.Errorf("version %d is not applied or is dirty", version)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx for down migration %d: %w", version, err)
	}

	_, err = tx.Exec(ctx, downSQL)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Errorf("down migration %d failed: %w", version, err)
	}

	// Remove the version record.
	_, err = tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, version)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Errorf("remove version %d record: %w", version, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit down migration %d: %w", version, err)
	}

	return nil
}
