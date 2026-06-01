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

// createIsolatedTestDB creates an isolated PostgreSQL schema and returns a
// connection pool configured with search_path pointing to that schema.
// This ensures all migrations and queries operate in isolation, even when
// the pool has multiple connections (avoids the SET search_path race where
// a pooled connection might not inherit the search_path set on another).
// Skips the test if AOS_TEST_DATABASE_URL / AOS_TEST_DSN is not set.
func createIsolatedTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("AOS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("AOS_TEST_DSN")
	}
	if dsn == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
		return nil
	}

	ctx := context.Background()

	// Connect with default search_path to create the test schema.
	adminPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to database: %v", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Fatalf("ping database: %v", err)
	}

	schema := fmt.Sprintf("test_mig_%s", regexp.MustCompile(`[^a-z0-9]`).ReplaceAllString(
		strconv.FormatUint(uint64(os.Getpid()), 10)+strings.ReplaceAll(t.Name(), "/", "_"), "_"))
	if _, err := adminPool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		adminPool.Close()
		t.Fatalf("create schema %s: %v", schema, err)
	}
	adminPool.Close()

	// Build a new pool with search_path baked into the connection config.
	// Every connection created by this pool will use the test schema.
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	config.MinConns = 0
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create isolated pool: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		// Reconnect with default search_path to drop the schema.
		dropPool, dropErr := pgxpool.New(ctx, dsn)
		if dropErr == nil {
			dropPool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %s CASCADE`, schema))
			dropPool.Close()
		}
	})

	return pool
}

// countRows is a test helper that counts rows matching the query.
func countRows(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&count); err != nil {
		t.Fatalf("countRows: %v", err)
	}
	return count
}

// tableExists checks if a table exists in the current schema (respects search_path).
func tableExists(t *testing.T, pool *pgxpool.Pool, tableName string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1)`,
		tableName,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("tableExists(%s): %v", tableName, err)
	}
	return exists
}

// ---------------------------------------------------------------------------
// Unit tests
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Integration tests (require PG17 via AOS_TEST_DATABASE_URL or AOS_TEST_DSN)
// ---------------------------------------------------------------------------

// TestMigrateUpFreshDB tests that all migrations apply cleanly to a fresh database.
func TestMigrateUpFreshDB(t *testing.T) {
	pool := createIsolatedTestDB(t)
	if pool == nil {
		return
	}
	ctx := context.Background()

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
	count := countRows(t, pool, `SELECT COUNT(*) FROM schema_migrations`)
	if count != expectedUp {
		t.Errorf("expected %d rows in schema_migrations, got %d", expectedUp, count)
	}

	// Verify no dirty entries.
	dirty := countRows(t, pool, `SELECT COUNT(*) FROM schema_migrations WHERE dirty = true`)
	if dirty != 0 {
		t.Errorf("expected 0 dirty migrations, got %d", dirty)
	}

	// Verify key tables exist.
	for _, tbl := range []string{"work_events", "projects", "tracker_items"} {
		if !tableExists(t, pool, tbl) {
			t.Errorf("expected table %s to exist", tbl)
		}
	}
}

// TestMigrateUpIdempotent verifies that running MigrateUp twice is safe.
func TestMigrateUpIdempotent(t *testing.T) {
	pool := createIsolatedTestDB(t)
	if pool == nil {
		return
	}
	ctx := context.Background()

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

// TestMigrateUpPartialDB simulates an existing database with some migrations
// already applied (like hpms1: versions 1,8-12 present, gaps 2-7 never existed).
// Seeds rows in row-per-version format and verifies only pending versions apply.
func TestMigrateUpPartialDB(t *testing.T) {
	pool := createIsolatedTestDB(t)
	if pool == nil {
		return
	}
	ctx := context.Background()

	// Create schema_migrations table and seed it with "already applied" versions
	// in row-per-version format.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, dirty boolean NOT NULL DEFAULT false);
		INSERT INTO schema_migrations (version, dirty) VALUES
			(1, false), (8, false), (9, false), (10, false), (11, false), (12, false);
	`); err != nil {
		t.Fatalf("seed partial state: %v", err)
	}

	// Also apply the actual SQL for those pre-seeded versions so the tables exist.
	allFiles, _ := parseMigrations()
	preApplied := map[int64]bool{1: true, 8: true, 9: true, 10: true, 11: true, 12: true}
	var preFiles []MigrationFile
	for _, f := range allFiles {
		if preApplied[f.Version] && regexp.MustCompile(`\.up\.sql$`).MatchString(f.Name) {
			preFiles = append(preFiles, f)
		}
	}
	sort.Slice(preFiles, func(i, j int) bool { return preFiles[i].Version < preFiles[j].Version })
	for _, f := range preFiles {
		if _, err := pool.Exec(ctx, f.Content); err != nil {
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
	totalCount := countRows(t, pool, `SELECT COUNT(*) FROM schema_migrations`)
	expected := len(preApplied) + result.Applied
	if totalCount != expected {
		t.Errorf("expected %d total versions, got %d", expected, totalCount)
	}
}

// TestMigrateDirtyAbort verifies that a dirty migration prevents further migrations.
func TestMigrateDirtyAbort(t *testing.T) {
	pool := createIsolatedTestDB(t)
	if pool == nil {
		return
	}
	ctx := context.Background()

	// Seed a dirty entry.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, dirty boolean NOT NULL DEFAULT false);
		INSERT INTO schema_migrations (version, dirty) VALUES (999, true);
	`); err != nil {
		t.Fatalf("seed dirty state: %v", err)
	}

	_, err := MigrateUp(ctx, pool)
	if err == nil {
		t.Fatal("expected MigrateUp to fail with dirty migration")
	}

	errMsg := err.Error()
	if !regexp.MustCompile(`999`).MatchString(errMsg) {
		t.Errorf("error should mention version 999, got: %v", err)
	}
	if !regexp.MustCompile(`dirty`).MatchString(errMsg) {
		t.Errorf("error should mention 'dirty', got: %v", err)
	}

	t.Logf("dirty abort error: %v", err)
}

// TestMigrateDirtyOnRealFailure verifies that a real migration SQL failure
// actually persists a dirty=true row in schema_migrations (regression for
// the bug where the dirty INSERT was in the same aborted transaction).
func TestMigrateDirtyOnRealFailure(t *testing.T) {
	pool := createIsolatedTestDB(t)
	if pool == nil {
		return
	}
	ctx := context.Background()

	// Create the tracking table.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, dirty boolean NOT NULL DEFAULT false);
	`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	// Apply all migrations up through 15 so only 16 is pending.
	allFiles, err := parseMigrations()
	if err != nil {
		t.Fatalf("parseMigrations: %v", err)
	}

	var preUp []MigrationFile
	for _, f := range allFiles {
		if f.Version <= 15 && strings.HasSuffix(f.Name, ".up.sql") {
			preUp = append(preUp, f)
		}
	}
	sort.Slice(preUp, func(i, j int) bool { return preUp[i].Version < preUp[j].Version })

	for _, f := range preUp {
		if _, err := pool.Exec(ctx, f.Content); err != nil {
			t.Fatalf("pre-apply migration %d: %v", f.Version, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO schema_migrations (version, dirty) VALUES ($1, false)`,
			f.Version,
		); err != nil {
			t.Fatalf("record migration %d: %v", f.Version, err)
		}
	}

	// Sabotage migration 16: it does CREATE TABLE tracker_items.
	// Pre-create it so the CREATE TABLE fails with "relation already exists".
	if _, err := pool.Exec(ctx, `CREATE TABLE tracker_items (id int)`); err != nil {
		t.Fatalf("sabotage pre-create table: %v", err)
	}

	// Run MigrateUp — migration 16 should fail and be marked dirty.
	_, err = MigrateUp(ctx, pool)
	if err == nil {
		t.Fatal("expected MigrateUp to fail on sabotaged migration 16")
	}
	t.Logf("MigrateUp error (expected): %v", err)

	// Assert (a): schema_migrations actually contains a dirty=true row for version 16.
	var dirtyVersion int64
	var dirty bool
	err = pool.QueryRow(ctx,
		`SELECT version, dirty FROM schema_migrations WHERE dirty = true`,
	).Scan(&dirtyVersion, &dirty)
	if err != nil {
		t.Fatalf("query dirty row: %v (no dirty row persisted — bug NOT fixed)", err)
	}
	if !dirty {
		t.Fatal("expected dirty=true, got false")
	}
	if dirtyVersion != 16 {
		t.Errorf("expected dirty version 16, got %d", dirtyVersion)
	}
	t.Logf("confirmed: version %d is persisted as dirty=true ✓", dirtyVersion)

	// Assert (b): a second MigrateUp aborts on the dirty state (not retries).
	_, err = MigrateUp(ctx, pool)
	if err == nil {
		t.Fatal("expected second MigrateUp to abort on dirty state")
	}
	errMsg := err.Error()
	if !regexp.MustCompile(`dirty`).MatchString(errMsg) {
		t.Errorf("second MigrateUp error should mention 'dirty', got: %v", err)
	}
	if !regexp.MustCompile(`16`).MatchString(errMsg) {
		t.Errorf("second MigrateUp error should mention version 16, got: %v", err)
	}
	t.Logf("second MigrateUp correctly aborted on dirty state: %v", err)
}

// TestMigrateUpWatermarkFormat verifies that MigrateUp correctly handles the
// golang-migrate watermark format (single non-dirty row whose version
// represents the highest applied migration). This is the format hpms1 likely
// uses after hand-applying migrations with golang-migrate.
func TestMigrateUpWatermarkFormat(t *testing.T) {
	pool := createIsolatedTestDB(t)
	if pool == nil {
		return
	}
	ctx := context.Background()

	// Manually apply migrations 1–12 SQL (simulating golang-migrate having run them).
	allFiles, err := parseMigrations()
	if err != nil {
		t.Fatalf("parseMigrations: %v", err)
	}

	// Create the tracking table.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, dirty boolean NOT NULL DEFAULT false);
	`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	// Apply migrations up to 12 manually (the SQL content, not through MigrateUp).
	for _, f := range allFiles {
		if f.Version <= 12 && strings.HasSuffix(f.Name, ".up.sql") {
			if _, err := pool.Exec(ctx, f.Content); err != nil {
				t.Fatalf("manual apply migration %d: %v", f.Version, err)
			}
		}
	}

	// Set a single watermark row (golang-migrate format).
	if _, err := pool.Exec(ctx, `TRUNCATE schema_migrations`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version, dirty) VALUES (12, false)`); err != nil {
		t.Fatalf("insert watermark: %v", err)
	}

	// Verify the watermark is in place: exactly 1 row.
	count := countRows(t, pool, `SELECT COUNT(*) FROM schema_migrations`)
	if count != 1 {
		t.Fatalf("expected 1 watermark row, got %d", count)
	}

	// Run MigrateUp — should detect watermark and apply only 13-16.
	result, err := MigrateUp(ctx, pool)
	if err != nil {
		t.Fatalf("MigrateUp with watermark: %v", err)
	}

	t.Logf("watermark test: applied %d pending migrations: %v", result.Applied, result.Versions)

	// Should have applied exactly 4 versions: 13, 14, 15, 16.
	expectedVersions := map[int64]bool{13: true, 14: true, 15: true, 16: true}
	if len(result.Versions) != len(expectedVersions) {
		t.Errorf("expected %d applied versions, got %d: %v", len(expectedVersions), len(result.Versions), result.Versions)
	}
	for _, v := range result.Versions {
		if !expectedVersions[v] {
			t.Errorf("unexpected applied version %d", v)
		}
		delete(expectedVersions, v)
	}
	for v := range expectedVersions {
		t.Errorf("expected version %d to be applied but wasn't", v)
	}

	// After watermark expansion, schema_migrations should have:
	// - Expansion rows for embedded versions <= 12 that exist: 1, 8, 9, 10, 11
	//   (version 12 conflicts with existing watermark row via ON CONFLICT DO NOTHING)
	// - The original watermark row (12)
	// - Newly applied rows: 13, 14, 15, 16
	// Total: 5 (expansion) + 1 (watermark) + 4 (new) = 10
	totalCount := countRows(t, pool, `SELECT COUNT(*) FROM schema_migrations`)
	if totalCount != 10 {
		t.Errorf("expected 10 rows in schema_migrations after watermark expansion + apply, got %d", totalCount)
	}

	// Verify second run is idempotent (now in row-per-version mode).
	r2, err := MigrateUp(ctx, pool)
	if err != nil {
		t.Fatalf("second MigrateUp after watermark expansion: %v", err)
	}
	if r2.Applied != 0 {
		t.Errorf("second run should apply 0, got %d", r2.Applied)
	}
}

// ---------------------------------------------------------------------------
// MigrateDown tests
// ---------------------------------------------------------------------------

// TestMigrateDownBasic verifies that MigrateDown correctly rolls back a migration:
// the down SQL runs, the row is removed, and subsequent MigrateUp re-applies it.
func TestMigrateDownBasic(t *testing.T) {
	pool := createIsolatedTestDB(t)
	if pool == nil {
		return
	}
	ctx := context.Background()

	// Apply all migrations.
	r1, err := MigrateUp(ctx, pool)
	if err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	if r1.Applied == 0 {
		t.Fatal("MigrateUp applied 0 migrations")
	}

	// Verify tracker_items exists before down.
	if !tableExists(t, pool, "tracker_items") {
		t.Fatal("tracker_items should exist before MigrateDown")
	}

	// MigrateDown version 16 (the highest).
	err = MigrateDown(ctx, pool, 16, "")
	if err != nil {
		t.Fatalf("MigrateDown(16): %v", err)
	}

	// Verify tracker_items is gone.
	if tableExists(t, pool, "tracker_items") {
		t.Error("tracker_items should NOT exist after MigrateDown(16)")
	}

	// Verify version 16 row is removed from schema_migrations.
	var exists bool
	err = pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = 16)`).
		Scan(&exists)
	if err != nil {
		t.Fatalf("check version 16: %v", err)
	}
	if exists {
		t.Error("version 16 should be removed from schema_migrations after MigrateDown")
	}

	// Total count should be original - 1.
	totalCount := countRows(t, pool, `SELECT COUNT(*) FROM schema_migrations`)
	expected := r1.Applied - 1
	if totalCount != expected {
		t.Errorf("expected %d rows in schema_migrations, got %d", expected, totalCount)
	}

	// Verify MigrateUp can re-apply the rolled-back migration.
	r3, err := MigrateUp(ctx, pool)
	if err != nil {
		t.Fatalf("MigrateUp after MigrateDown: %v", err)
	}
	if r3.Applied != 1 || r3.Versions[0] != 16 {
		t.Errorf("expected MigrateUp to re-apply version 16, got: applied=%d versions=%v", r3.Applied, r3.Versions)
	}

	t.Logf("MigrateDown(16): table gone, row removed, re-applied on next MigrateUp ✓")
}

// TestMigrateDownRefusesDirty verifies that MigrateDown refuses a dirty version.
func TestMigrateDownRefusesDirty(t *testing.T) {
	pool := createIsolatedTestDB(t)
	if pool == nil {
		return
	}

	// Apply all migrations.
	if _, err := MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}

	// Mark version 16 as dirty.
	if _, err := pool.Exec(context.Background(),
		`UPDATE schema_migrations SET dirty = true WHERE version = 16`); err != nil {
		t.Fatalf("mark dirty: %v", err)
	}

	// MigrateDown should refuse.
	err := MigrateDown(context.Background(), pool, 16, "")
	if err == nil {
		t.Fatal("expected MigrateDown to refuse dirty version")
	}
	if !strings.Contains(err.Error(), "not applied or is dirty") {
		t.Errorf("expected 'not applied or is dirty' error, got: %v", err)
	}
	t.Logf("MigrateDown correctly refused dirty version: %v ✓", err)
}

// TestMigrateDownRefusesAbsent verifies that MigrateDown refuses a version
// that was never applied.
func TestMigrateDownRefusesAbsent(t *testing.T) {
	pool := createIsolatedTestDB(t)
	if pool == nil {
		return
	}

	// Apply all migrations.
	if _, err := MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}

	// Try to MigrateDown a version that doesn't exist in schema_migrations.
	err := MigrateDown(context.Background(), pool, 999, "")
	if err == nil {
		t.Fatal("expected MigrateDown to refuse absent version")
	}
	if !strings.Contains(err.Error(), "not applied or is dirty") {
		t.Errorf("expected 'not applied or is dirty' error, got: %v", err)
	}
	t.Logf("MigrateDown correctly refused absent version: %v ✓", err)
}
