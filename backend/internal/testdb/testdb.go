// Package testdb provisions an isolated PostgreSQL/PostGIS schema for tests
// that need to exercise real concurrency behavior (row locks, leases,
// constraints) instead of mocks. See docs/progresso.md for why these tests
// are opt-in rather than part of the default `go test ./...` run.
package testdb

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"agendaos/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
)

var migrationName = regexp.MustCompile(`^(\d+)_.*\.sql$`)

// New opens a Store scoped to a fresh, uniquely named schema on
// TEST_DATABASE_URL and applies every file under migrations/ into it. Tests
// call this when they need real PostgreSQL/PostGIS semantics (row locking,
// advisory locks, constraints); it skips the test when the variable is not
// set so `go test ./...` stays usable without a database.
//
// TEST_DATABASE_URL must point at the same postgis/postgis image the project
// runs elsewhere (see compose.yaml); run scripts/postgres-tests.sh to start
// one and set the variable automatically.
func New(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL não definido; teste requer PostgreSQL/PostGIS real (veja scripts/postgres-tests.sh)")
	}
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("conectar ao banco de teste: %v", err)
	}
	t.Cleanup(admin.Close)

	schema := fmt.Sprintf("test_%d_%d", time.Now().UnixNano(), rand.Intn(1_000_000))
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoteIdent(schema)); err != nil {
		t.Fatalf("criar schema de teste: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quoteIdent(schema)+" CASCADE"); err != nil {
			t.Logf("limpar schema de teste %s: %v", schema, err)
		}
	})

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("configurar pool de teste: %v", err)
	}
	// Every pooled connection resolves unqualified migration objects (types,
	// tables) against this schema first; extensions such as postgis stay
	// shared at the database level and are still visible through public.
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("abrir pool de teste: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := applyMigrations(ctx, pool); err != nil {
		t.Fatalf("aplicar migrações: %v", err)
	}
	return &store.Store{Pool: pool}
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// testMigrationLock is an arbitrary, distinct id from the one cmd/migrate
// uses in production. Extensions such as postgis are database-wide, so
// `CREATE EXTENSION IF NOT EXISTS` from two test binaries racing against the
// same TEST_DATABASE_URL (go test runs each package's tests as a separate
// process, in parallel) can both pass the existence check and then collide
// inserting into pg_extension. The advisory lock serializes migration
// application across those processes; each still gets its own schema.
const testMigrationLock = 684672130946

func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	dir := migrationsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("ler %s: %w", dir, err)
	}
	type file struct {
		version string
		path    string
	}
	files := make([]file, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := migrationName.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		files = append(files, file{version: match[1], path: filepath.Join(dir, entry.Name())})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].version < files[j].version })
	if len(files) == 0 {
		return fmt.Errorf("nenhuma migração encontrada em %s", dir)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("reservar conexão para migrar: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(testMigrationLock)); err != nil {
		return fmt.Errorf("obter trava de migração: %w", err)
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, int64(testMigrationLock))

	for _, item := range files {
		sql, err := os.ReadFile(item.path)
		if err != nil {
			return err
		}
		// Each file wraps its own BEGIN/COMMIT, so it can run as-is; there is
		// no schema_migrations bookkeeping here because every test starts a
		// disposable schema instead of reusing an existing database.
		if _, err := conn.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("executar %s: %w", filepath.Base(item.path), err)
		}
	}
	return nil
}

// migrationsDir resolves the repository's migrations/ directory regardless
// of which package under backend/ is running the test, so tests do not break
// when moved between internal/ subpackages.
func migrationsDir() string {
	if override := os.Getenv("TEST_MIGRATIONS_DIR"); override != "" {
		return override
	}
	dir, err := os.Getwd()
	if err != nil {
		return "migrations"
	}
	for {
		candidate := filepath.Join(dir, "migrations")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "migrations"
		}
		dir = parent
	}
}
