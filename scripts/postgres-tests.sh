#!/bin/sh
set -eu

# Runs the Go test suite — including the PostgreSQL/PostGIS concurrency
# tests under backend/internal/store and backend/internal/sync — against a
# disposable database, the same way scripts/integration-test.sh exercises
# the full stack. Those tests are skipped by a plain `go test ./...` because
# they need TEST_DATABASE_URL; this script starts a database, sets that
# variable, and tears everything down afterward.

command -v docker >/dev/null || { echo "docker é obrigatório" >&2; exit 1; }

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
PROJECT="agenda-os-pgtest-$$"
NETWORK="${PROJECT}_default"
export POSTGRES_PASSWORD="postgres_tests_password"

cleanup() {
  docker compose -p "$PROJECT" -f "$ROOT_DIR/compose.yaml" down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

docker compose -p "$PROJECT" -f "$ROOT_DIR/compose.yaml" up -d db

attempt=0
until docker compose -p "$PROJECT" -f "$ROOT_DIR/compose.yaml" exec -T db pg_isready -U agenda_os -d agenda_os >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  [ "$attempt" -lt 30 ] || { echo "banco de teste não ficou pronto" >&2; exit 1; }
  sleep 1
done

docker build --target build -f "$ROOT_DIR/backend/Dockerfile" -t agenda-os-postgres-tests "$ROOT_DIR"

docker run --rm --network "$NETWORK" \
  -e TEST_DATABASE_URL="postgres://agenda_os:${POSTGRES_PASSWORD}@db:5432/agenda_os?sslmode=disable" \
  -w /src agenda-os-postgres-tests \
  sh -c 'apk add --no-cache build-base >/dev/null 2>&1; CGO_ENABLED=1 go test ./... -race -count=1 -v'

echo "testes PostgreSQL aprovados, incluindo concorrência de tarefas e sincronização"
