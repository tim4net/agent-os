package db

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tim4net/agent-os/internal/migrations"
)

// MigrateAdvisoryLockID is the Postgres advisory lock ID used to serialize
// migration runs. The value (3854494529) is a well-known constant scoped to
// the whole database. All migration functions (up and down) take this lock
// on a dedicated connection to prevent concurrent runs across API replicas.
const MigrateAdvisoryLockID int64 = 3854494529

// dbExecutor abstracts Exec/Query/QueryRow shared by *pgxpool.Pool,
// *pgxpool.Conn, and pgx.Tx so helper functions can operate on any
// of them without coupling to a concrete type.
type dbExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

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
func ensureSchemaMigrationsTable(ctx context.Context, db dbExecutor) error {
	_, err := db.Exec(ctx, `
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

// appliedSet is the result of computing which migration versions are applied.
type appliedSet struct {
	Versions         map[int64]bool // set of applied (non-dirty) versions
	IsWatermark      bool           // true if the tracking table uses golang-migrate watermark format
	WatermarkVersion int64          // the watermark value (0 if not watermark)
}

// computeAppliedVersions reads schema_migrations and determines which versions
// are considered applied. It handles both formats:
//   - Row-per-version (our native format): each applied version has its own row.
//   - golang-migrate watermark: a single non-dirty row whose version represents
//     the highest applied migration. All embedded versions <= that watermark
//     are treated as applied.
//
// If the table is empty, no versions are considered applied.
func computeAppliedVersions(ctx context.Context, db dbExecutor, allFiles []MigrationFile) (*appliedSet, error) {
	rows, err := db.Query(ctx, `SELECT version, dirty FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	type rawRow struct {
		Version int64
		Dirty   bool
	}
	var raw []rawRow
	for rows.Next() {
		var r rawRow
		if err := rows.Scan(&r.Version, &r.Dirty); err != nil {
			return nil, fmt.Errorf("scan schema_migrations row: %w", err)
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := &appliedSet{Versions: make(map[int64]bool)}

	switch len(raw) {
	case 0:
		// Empty table — fresh DB, no migrations applied.
	case 1:
		if !raw[0].Dirty {
			// golang-migrate watermark: single non-dirty row means all
			// embedded versions up to and including this value are applied.
			//
			// Heuristic scope: this interpretation is correct for the current
			// deployment (hpms1 is the only live DB, managed by golang-migrate,
			// which stores exactly one watermark row). The expansion is
			// atomic (single tx inside the advisory lock) and the assumed
			// version list is logged, so a misfire is detectable. A future
			// native single-row state or mixed-format scenario should be
			// gated behind an explicit "migrate adopt" subcommand rather than
			// extending this heuristic.
			result.IsWatermark = true
			result.WatermarkVersion = raw[0].Version
			for _, f := range allFiles {
				if f.Version <= raw[0].Version {
					result.Versions[f.Version] = true
				}
			}
		}
		// Single dirty row — nothing is considered clean.
	default:
		// Row-per-version format (our native): each non-dirty row is applied.
		for _, r := range raw {
			if !r.Dirty {
				result.Versions[r.Version] = true
			}
		}
	}

	return result, nil
}

// dirtyVersions reads the set of dirty migration versions.
func dirtyVersions(ctx context.Context, db dbExecutor) (map[int64]bool, error) {
	rows, err := db.Query(ctx, `SELECT version FROM schema_migrations WHERE dirty = true`)
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

// markDirty records a migration version as dirty using the given executor.
// This MUST be called after the failed migration's transaction is already rolled back,
// because Postgres aborts a transaction after any statement error.
func markDirty(ctx context.Context, db dbExecutor, version int64) error {
	_, err := db.Exec(ctx,
		`INSERT INTO schema_migrations(version, dirty) VALUES($1, true) ON CONFLICT (version) DO UPDATE SET dirty = true`,
		version,
	)
	if err != nil {
		return fmt.Errorf("mark migration %d dirty: %w", version, err)
	}
	return nil
}

// MigrateResult holds the outcome of a MigrateUp call.
type MigrateResult struct {
	Applied  int      // number of migrations applied this run
	Versions []int64  // versions that were applied
}

// MigrateUp applies all pending up-migrations to the database.
// Each migration runs in its own transaction. On failure, the transaction
// rolls back and the version is recorded as dirty=true (aborting further
// migrations). Gaps in version numbers are handled naturally — only
// embedded files that exist are applied.
//
// This function is safe to call at server boot or via a CLI subcommand.
// It takes a Postgres advisory lock (session-scoped, on a dedicated connection)
// to prevent concurrent migration runs across multiple API replicas.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool) (*MigrateResult, error) {
	return MigrateUpWithLogger(ctx, pool, nil)
}

// MigrateUpWithLogger is like MigrateUp but accepts an optional logger.
func MigrateUpWithLogger(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (*MigrateResult, error) {
	log := logger
	if log == nil {
		log = slog.Default()
	}

	// Acquire a dedicated connection for the entire migration run.
	// This is critical for the advisory lock: pg_advisory_lock is
	// session-scoped, so the lock, all migration transactions, and the
	// unlock MUST run on the same connection. Using pool.Exec for each
	// would land on different pooled connections, causing silent unlock
	// failures and lock leaks.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection for migration: %w", err)
	}
	defer conn.Release()

	if err := ensureSchemaMigrationsTable(ctx, conn); err != nil {
		return nil, err
	}

	// Take a Postgres advisory lock to prevent concurrent migration runs
	// (e.g. multiple API replicas booting simultaneously).
	_, err = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, MigrateAdvisoryLockID)
	if err != nil {
		return nil, fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	defer func() {
		// Use context.Background() for the unlock — cleanup must not be
		// cancellable. If the parent ctx is cancelled/deadlined at cleanup
		// time (e.g. a signal-cancellable context from main()), the unlock
		// query would fail with "context canceled" on the old code, the
		// lock-holding connection would be returned to the pool, and a
		// subsequent MigrateUp on another replica would block indefinitely.
		var ok bool
		unlockErr := conn.QueryRow(context.Background(), `SELECT pg_advisory_unlock($1)`, MigrateAdvisoryLockID).Scan(&ok)
		if unlockErr != nil || !ok {
			log.Error("advisory unlock failed or returned false — destroying connection to prevent lock leak",
				"error", unlockErr, "ok", ok)
			// Close the underlying connection so the lock-holding conn is
			// destroyed, not returned to the pool. conn.Release() (outer
			// defer) will see the conn is closed and handle it gracefully.
			conn.Conn().Close(context.Background())
		}
	}()

	// ---- All TOCTOU-sensitive reads happen INSIDE the lock ----

	// Check for existing dirty state.
	dirty, err := dirtyVersions(ctx, conn)
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

	// Compute the applied version set, handling both row-per-version and
	// golang-migrate watermark formats.
	allFiles, err := parseMigrations()
	if err != nil {
		return nil, err
	}
	appliedResult, err := computeAppliedVersions(ctx, conn, allFiles)
	if err != nil {
		return nil, err
	}

	// If the tracking table uses golang-migrate watermark format (single
	// non-dirty row), expand it to row-per-version entries for forward
	// compatibility. This ensures subsequent runs in row-per-version mode
	// (triggered by >1 rows) see all applied versions. This runs inside the
	// advisory lock so it's serialized. The expansion is wrapped in a single
	// transaction so it's atomic — a crash mid-expansion cannot leave partial
	// state that would cause a subsequent boot to re-run already-applied
	// migrations (which would choke on existing tables).
	if appliedResult.IsWatermark {
		// Log the exact assumed-applied version list so an operator can catch
		// a bad assumption (e.g. if the watermark value is unexpectedly high).
		assumedVersions := make([]int64, 0, len(appliedResult.Versions))
		for v := range appliedResult.Versions {
			assumedVersions = append(assumedVersions, v)
		}
		sort.Slice(assumedVersions, func(i, j int) bool { return assumedVersions[i] < assumedVersions[j] })
		log.Info("detected golang-migrate watermark format — expanding to row-per-version",
			"watermark", appliedResult.WatermarkVersion,
			"assumed_applied", assumedVersions)

		// Wrap the entire expansion in a transaction for atomicity.
		tx, err := conn.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("begin watermark expansion tx: %w", err)
		}
		for _, f := range allFiles {
			if f.Version <= appliedResult.WatermarkVersion && strings.HasSuffix(f.Name, ".up.sql") {
				if _, err := tx.Exec(ctx,
					`INSERT INTO schema_migrations(version, dirty) VALUES($1, false) ON CONFLICT (version) DO NOTHING`,
					f.Version,
				); err != nil {
					if rbErr := tx.Rollback(ctx); rbErr != nil {
						log.Error("failed to rollback watermark expansion tx", "error", rbErr)
					}
					return nil, fmt.Errorf("expand watermark version %d: %w", f.Version, err)
				}
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit watermark expansion: %w", err)
		}
		// The watermark row is now redundant but harmless — keeping it avoids
		// a pointless DELETE that could fail if something goes wrong.
	}

	applied := appliedResult.Versions

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

		// Begin a transaction for this migration on the dedicated connection.
		tx, err := conn.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("begin tx for migration %d: %w", f.Version, err)
		}

		// Execute the migration SQL.
		_, err = tx.Exec(ctx, f.Content)
		if err != nil {
			// The migration SQL failed — the transaction is now aborted in Postgres.
			// Roll back the failed tx, then record dirty=true on the dedicated conn
			// (which is still usable after the rollback).
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				log.Error("failed to rollback failed migration tx",
					"version", f.Version, "error", rbErr)
			}
			if markErr := markDirty(ctx, conn, f.Version); markErr != nil {
				log.Error("failed to mark migration dirty after failure",
					"version", f.Version, "originalError", err, "markError", markErr)
				return nil, fmt.Errorf("migration %d (%s) failed: %w (AND failed to mark dirty: %v)", f.Version, f.Name, err, markErr)
			}
			return nil, fmt.Errorf("migration %d (%s) failed: %w (marked dirty — fix manually)", f.Version, f.Name, err)
		}

		// Record the version as clean (atomic with the migration SQL in the same tx).
		_, err = tx.Exec(ctx,
			`INSERT INTO schema_migrations(version, dirty) VALUES($1, false) ON CONFLICT (version) DO UPDATE SET dirty = false`,
			f.Version,
		)
		if err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				log.Error("failed to rollback tx after record failure",
					"version", f.Version, "error", rbErr)
			}
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

// MigrateDown rolls back a single migration. This is ONLY for explicit
// operator use — never called automatically. The caller must provide the
// exact version to roll back (or empty string to auto-lookup the down SQL
// from embedded files).
//
// Takes the same Postgres advisory lock as MigrateUp to prevent concurrent
// down/up interleaving across API replicas.
func MigrateDown(ctx context.Context, pool *pgxpool.Pool, version int64, downSQL string) error {
	// Acquire a dedicated connection for the advisory lock (same pattern as
	// MigrateUp — pg_advisory_lock is session-scoped).
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for MigrateDown: %w", err)
	}
	defer conn.Release()

	if err := ensureSchemaMigrationsTable(ctx, conn); err != nil {
		return err
	}

	// Take the same advisory lock as MigrateUp.
	_, err = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, MigrateAdvisoryLockID)
	if err != nil {
		return fmt.Errorf("acquire migration advisory lock for down: %w", err)
	}
	defer func() {
		// Use context.Background() for the unlock — same rationale as
		// MigrateUp. Cleanup must not be cancellable.
		var ok bool
		unlockErr := conn.QueryRow(context.Background(), `SELECT pg_advisory_unlock($1)`, MigrateAdvisoryLockID).Scan(&ok)
		if unlockErr != nil || !ok {
			slog.Default().Error("advisory unlock failed or returned false (down) — destroying connection to prevent lock leak",
				"error", unlockErr, "ok", ok)
			conn.Conn().Close(context.Background())
		}
	}()

	// Check that the version is currently applied and not dirty FIRST.
	// This must happen inside the lock (TOCTOU) and before the down-SQL
	// lookup so the operator gets a consistent "not applied or is dirty"
	// error for absent versions too.
	var exists bool
	err = conn.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1 AND dirty = false)`,
		version,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check version %d: %w", version, err)
	}
	if !exists {
		return fmt.Errorf("version %d is not applied or is dirty", version)
	}

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

	// Begin the down migration transaction on the dedicated connection.
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx for down migration %d: %w", version, err)
	}

	_, err = tx.Exec(ctx, downSQL)
	if err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			slog.Default().Error("failed to rollback down migration tx",
				"version", version, "error", rbErr)
		}
		return fmt.Errorf("down migration %d failed: %w", version, err)
	}

	// Remove the version record.
	_, err = tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, version)
	if err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			slog.Default().Error("failed to rollback tx after delete failure in down migration",
				"version", version, "error", rbErr)
		}
		return fmt.Errorf("remove version %d record: %w", version, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit down migration %d: %w", version, err)
	}

	return nil
}
