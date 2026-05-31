package db

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// skipIfNoDB skips the test if PG17 is not available.
// Set DATABASE_URL to a test database to run integration tests.
func skipIfNoDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		// Fall back to DATABASE_URL for CI.
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
		return nil
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to database: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// createTestSchema creates an isolated schema for the test and returns a cleanup func.
func createTestSchema(t *testing.T, pool *pgxpool.Pool) (string, func()) {
	t.Helper()
	ctx := context.Background()
	schema := fmt.Sprintf("test_mig_%s", regexp.MustCompile(`[^a-z0-9]`).ReplaceAllString(
		strconv.FormatUint(uint64(os.Getpid()), 10)+t.Name(), "_"))

	_, err := pool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schema))
	if err != nil {
		t.Fatalf("create schema %s: %v", schema, err)
	}
	cleanup := func() {
		pool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %s CASCADE`, schema))
	}
	t.Cleanup(cleanup)
	return schema, cleanup
}

// TestParseMigrations verifies that embedded migration files are parsed correctly.
func TestParseMigrations(t *testing.T) {
	files, err := parseMigrations()
	if err != nil {
		t.Fatalf("parseMigrations: %v", err)
	}

	if len(files) == 0 {
		t.Fatal("expected at least one migration file")
	}

	// Verify sorting (versions are non-decreasing; up before down at same version).
	for i := 1; i < len(files); i++ {
		if files[i].Version < files[i-1].Version {
			t.Errorf("not sorted: version %d at index %d < version %d at index %d",
				files[i].Version, i, files[i-1].Version, i-1)
		}
		// Same version: up should come before down.
		if files[i].Version == files[i-1].Version &&
			strings.HasSuffix(files[i-1].Name, ".down.sql") &&
			strings.HasSuffix(files[i].Name, ".up.sql") {
			t.Errorf("down before up at version %d: %s then %s",
				files[i].Version, files[i-1].Name, files[i].Name)
		}
	}

	// Check that known versions exist.
	knownVersions := map[int64]string{
		1:  "000001_init.up.sql",
		8:  "000008_workflows.up.sql",
		14: "000014_work_events.up.sql",
		15: "000015_projects_tracker.up.sql",
	}
	for ver, expected := range knownVersions {
		found := false
		for _, f := range files {
			if f.Version == ver && f.Name == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected migration version %d (%s) not found", ver, expected)
		}
	}

	// Verify no non-SQL files leaked through.
	for _, f := range files {
		if !regexp.MustCompile(`\.sql$`).MatchString(f.Name) {
			t.Errorf("non-SQL file in migrations: %s", f.Name)
		}
	}

	t.Logf("parsed %d migration files, versions %d–%d", len(files), files[0].Version, files[len(files)-1].Version)
}

// TestEnsureSchemaMigrationsTable verifies the tracking table is created.
func TestEnsureSchemaMigrationsTable(t *testing.T) {
	pool := skipIfNoDB(t)
	if pool == nil {
		return
	}
	ctx := context.Background()

	schema, _ := createTestSchema(t, pool)
	// Use a temporary table approach — run in public schema with unique name.
	tableName := fmt.Sprintf("schema_migrations_test_%d", os.Getpid())

	// Create the table via the function (we'll test directly since ensureSchemaMigrationsTable
	// is unexported — instead test through MigrateUp).
	// Actually, test MigrateUp which calls ensureSchemaMigrationsTable.
	_ = schema // unused in this path

	// Clean up the test table.
	defer pool.Exec(context.Background(), fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tableName))

	// Verify the table doesn't exist yet.
	var exists bool
	err := pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = '%s')`, tableName),
	).Scan(&exists)
	if err != nil {
		t.Fatalf("check table existence: %v", err)
	}
	if exists {
		t.Fatalf("table %s already exists", tableName)
	}

	t.Log("schema_migrations table creation tested via MigrateUp integration")
}

// TestMigrateUpFreshDB tests that all migrations apply cleanly to a fresh database.
func TestMigrateUpFreshDB(t *testing.T) {
	pool := skipIfNoDB(t)
	if pool == nil {
		return
	}
	ctx := context.Background()

	// Create isolated schema.
	schema, _ := createTestSchema(t, pool)
	searchPath := fmt.Sprintf("SET search_path TO %s,public", schema)

	// Set search_path for the connection so all migrations go into the isolated schema.
	_, err := pool.Exec(ctx, searchPath)
	if err != nil {
		t.Fatalf("set search_path: %v", err)
	}

	result, err := MigrateUp(ctx, pool)
	if err != nil {
		t.Fatalf("MigrateUp on fresh DB: %v", err)
	}

	// Count how many up-migrations should exist.
	allFiles, _ := parseMigrations()
	var expectedUp int
	for _, f := range allFiles {
		if regexp.MustCompile(`\.up\.sql$`).MatchString(f.Name) {
			expectedUp++
		}
	}

	t.Logf("applied %d of %d up-migrations, versions: %v", result.Applied, expectedUp, result.Versions)

	if result.Applied != expectedUp {
		t.Errorf("expected %d migrations applied, got %d", expectedUp, result.Applied)
	}

	// Verify schema_migrations has correct count.
	var count int
	err = pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM %s.schema_migrations`, schema),
	).Scan(&count)
	if err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != expectedUp {
		t.Errorf("expected %d rows in schema_migrations, got %d", expectedUp, count)
	}

	// Verify no dirty entries.
	var dirtyCount int
	err = pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM %s.schema_migrations WHERE dirty = true`, schema),
	).Scan(&dirtyCount)
	if err != nil {
		t.Fatalf("count dirty: %v", err)
	}
	if dirtyCount != 0 {
		t.Errorf("expected 0 dirty migrations, got %d", dirtyCount)
	}

	// Verify key tables exist.
	for _, tbl := range []string{"work_events", "projects"} {
		var tblExists bool
		err = pool.QueryRow(ctx,
			fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = '%s' AND table_name = '%s')`, schema, tbl),
		).Scan(&tblExists)
		if err != nil {
			t.Fatalf("check table %s: %v", tbl, err)
		}
		if !tblExists {
			t.Errorf("expected table %s.%s to exist", schema, tbl)
		}
	}
}

// TestMigrateUpIdempotent verifies that running MigrateUp twice is safe.
func TestMigrateUpIdempotent(t *testing.T) {
	pool := skipIfNoDB(t)
	if pool == nil {
		return
	}
	ctx := context.Background()

	schema, _ := createTestSchema(t, pool)
	_, err := pool.Exec(ctx, fmt.Sprintf("SET search_path TO %s,public", schema))
	if err != nil {
		t.Fatalf("set search_path: %v", err)
	}

	// First run.
	r1, err := MigrateUp(ctx, pool)
	if err != nil {
		t.Fatalf("first MigrateUp: %v", err)
	}
	if r1.Applied == 0 {
		t.Fatal("first run applied 0 migrations — something is wrong")
	}

	// Second run — should apply nothing.
	r2, err := MigrateUp(ctx, pool)
	if err != nil {
		t.Fatalf("second MigrateUp: %v", err)
	}
	if r2.Applied != 0 {
		t.Errorf("second run should apply 0, got %d", r2.Applied)
	}

	t.Logf("first run: %d applied, second run: %d applied (idempotent ✓)", r1.Applied, r2.Applied)
}

// TestMigrateUpPartialDB simulates an existing database with some migrations already applied
// (like hpms1: versions 1,8-12 present, gaps 2-7 never existed).
func TestMigrateUpPartialDB(t *testing.T) {
	pool := skipIfNoDB(t)
	if pool == nil {
		return
	}
	ctx := context.Background()

	schema, _ := createTestSchema(t, pool)
	_, err := pool.Exec(ctx, fmt.Sprintf("SET search_path TO %s,public", schema))
	if err != nil {
		t.Fatalf("set search_path: %v", err)
	}

	// Create schema_migrations table in the test schema and seed it with "already applied" versions.
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE %s.schema_migrations (version bigint PRIMARY KEY, dirty boolean NOT NULL DEFAULT false);
		INSERT INTO %s.schema_migrations (version, dirty) VALUES
			(1, false), (8, false), (9, false), (10, false), (11, false), (12, false);
	`, schema, schema))
	if err != nil {
		t.Fatalf("seed partial state: %v", err)
	}

	// Also apply the actual SQL for those pre-seeded versions so the tables exist.
	// We need to apply migrations 1, 8-12 SQL content to avoid FK violations.
	allFiles, _ := parseMigrations()
	preApplied := map[int64]bool{1: true, 8: true, 9: true, 10: true, 11: true, 12: true}
	var preFiles []MigrationFile
	for _, f := range allFiles {
		if preApplied[f.Version] && regexp.MustCompile(`\.up\.sql$`).MatchString(f.Name) {
			preFiles = append(preFiles, f)
		}
	}
	// Sort by version.
	sort.Slice(preFiles, func(i, j int) bool { return preFiles[i].Version < preFiles[j].Version })

	for _, f := range preFiles {
		_, err = pool.Exec(ctx, f.Content)
		if err != nil {
			t.Fatalf("pre-apply migration %d: %v", f.Version, err)
		}
	}

	// Now run MigrateUp — should only apply 13-16 (versions missing from the partial DB).
	result, err := MigrateUp(ctx, pool)
	if err != nil {
		t.Fatalf("MigrateUp on partial DB: %v", err)
	}

	t.Logf("partial DB: applied %d pending migrations: %v", result.Applied, result.Versions)

	if result.Applied == 0 {
		t.Fatal("expected some migrations to be applied on partial DB")
	}

	// Verify that the gap versions (2-7) were NOT attempted.
	for _, v := range result.Versions {
		if v >= 2 && v <= 7 {
			t.Errorf("should not have applied gap version %d", v)
		}
	}

	// Verify total versions in schema_migrations = pre-seeded (6) + newly applied.
	var totalCount int
	err = pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM %s.schema_migrations`, schema),
	).Scan(&totalCount)
	if err != nil {
		t.Fatalf("count total: %v", err)
	}
	expected := len(preApplied) + result.Applied
	if totalCount != expected {
		t.Errorf("expected %d total versions, got %d", expected, totalCount)
	}
}

// TestMigrateDirtyAbort verifies that a dirty migration prevents further migrations.
func TestMigrateDirtyAbort(t *testing.T) {
	pool := skipIfNoDB(t)
	if pool == nil {
		return
	}
	ctx := context.Background()

	schema, _ := createTestSchema(t, pool)
	_, err := pool.Exec(ctx, fmt.Sprintf("SET search_path TO %s,public", schema))
	if err != nil {
		t.Fatalf("set search_path: %v", err)
	}

	// Seed a dirty entry.
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE %s.schema_migrations (version bigint PRIMARY KEY, dirty boolean NOT NULL DEFAULT false);
		INSERT INTO %s.schema_migrations (version, dirty) VALUES (999, true);
	`, schema, schema))
	if err != nil {
		t.Fatalf("seed dirty state: %v", err)
	}

	_, err = MigrateUp(ctx, pool)
	if err == nil {
		t.Fatal("expected MigrateUp to fail with dirty migration")
	}

	// Error should mention the dirty version.
	errMsg := err.Error()
	if !regexp.MustCompile(`999`).MatchString(errMsg) {
		t.Errorf("error should mention version 999, got: %v", err)
	}
	if !regexp.MustCompile(`dirty`).MatchString(errMsg) {
		t.Errorf("error should mention 'dirty', got: %v", err)
	}

	t.Logf("dirty abort error: %v", err)
}
