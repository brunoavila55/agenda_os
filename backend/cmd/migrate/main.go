package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"agendaos/internal/config"
	"agendaos/internal/store"
	"github.com/jackc/pgx/v5"
)

var migrationName = regexp.MustCompile(`^(\d+)_.*\.sql$`)

type migration struct {
	Version  string
	Name     string
	Checksum string
	SQL      string
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuração inválida", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	dataStore, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("banco indisponível", "error", err)
		os.Exit(1)
	}
	defer dataStore.Pool.Close()
	directory := os.Getenv("MIGRATIONS_DIR")
	if directory == "" {
		directory = "/app/migrations"
	}
	items, err := loadMigrations(directory)
	if err != nil {
		logger.Error("ler migrações", "error", err)
		os.Exit(1)
	}
	if err := apply(ctx, dataStore, items, logger); err != nil {
		logger.Error("aplicar migrações", "error", err)
		os.Exit(1)
	}
	logger.Info("migrações atualizadas", "count", len(items))
}

func loadMigrations(directory string) ([]migration, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	items := make([]migration, 0)
	seen := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := migrationName.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		if seen[match[1]] {
			return nil, fmt.Errorf("versão de migração duplicada: %s", match[1])
		}
		seen[match[1]] = true
		contents, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(contents)
		items = append(items, migration{Version: match[1], Name: entry.Name(), Checksum: hex.EncodeToString(digest[:]), SQL: string(contents)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Version < items[j].Version })
	return items, nil
}

func apply(ctx context.Context, dataStore *store.Store, migrations []migration, logger *slog.Logger) error {
	connection, err := dataStore.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock(684672130945)`); err != nil {
		return err
	}
	defer connection.Exec(context.Background(), `SELECT pg_advisory_unlock(684672130945)`)
	if _, err := connection.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY, name text NOT NULL, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}
	if err := baselineKnownMigrations(ctx, connection.Conn(), migrations, logger); err != nil {
		return err
	}
	for _, item := range migrations {
		var checksum string
		err := connection.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE version=$1`, item.Version).Scan(&checksum)
		if err == nil {
			if checksum != item.Checksum {
				return fmt.Errorf("migração %s foi alterada depois de aplicada", item.Name)
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		tx, err := connection.Begin(ctx)
		if err != nil {
			return err
		}
		sql := transactionBody(item.SQL)
		if _, err := tx.Exec(ctx, sql); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("executar %s: %w", item.Name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version,name,checksum) VALUES ($1,$2,$3)`, item.Version, item.Name, item.Checksum); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		logger.Info("migração aplicada", "version", item.Version, "name", item.Name)
	}
	return nil
}

// The first releases used docker-entrypoint-initdb.d without a tracking table.
// Detect those two immutable schemas once so existing volumes can adopt the
// runner without replaying CREATE TYPE/TABLE statements.
func baselineKnownMigrations(ctx context.Context, connection *pgx.Conn, migrations []migration, logger *slog.Logger) error {
	var count int
	if err := connection.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil || count != 0 {
		return err
	}
	var initialExists bool
	if err := connection.QueryRow(ctx, `SELECT to_regclass('public.operations') IS NOT NULL`).Scan(&initialExists); err != nil || !initialExists {
		return err
	}
	for _, item := range migrations {
		baseline := item.Version == "001"
		if item.Version == "002" {
			if err := connection.QueryRow(ctx, `SELECT EXISTS(
				SELECT 1 FROM pg_constraint WHERE conname='proposal_items_group_id_fkey' AND confdeltype='n'
			)`).Scan(&baseline); err != nil {
				return err
			}
		}
		if !baseline {
			continue
		}
		if _, err := connection.Exec(ctx, `INSERT INTO schema_migrations (version,name,checksum) VALUES ($1,$2,$3)`, item.Version, item.Name, item.Checksum); err != nil {
			return err
		}
		logger.Info("migração preexistente registrada", "version", item.Version, "name", item.Name)
	}
	return nil
}

func transactionBody(sql string) string {
	trimmed := strings.TrimSpace(sql)
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "BEGIN;") {
		trimmed = strings.TrimSpace(trimmed[len("BEGIN;"):])
		upper = strings.ToUpper(trimmed)
	}
	if strings.HasSuffix(upper, "COMMIT;") {
		trimmed = strings.TrimSpace(trimmed[:len(trimmed)-len("COMMIT;")])
	}
	return trimmed
}
