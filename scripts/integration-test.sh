#!/bin/sh
set -eu

command -v docker >/dev/null || { echo "docker é obrigatório" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq é obrigatório" >&2; exit 1; }

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
PROJECT="agenda-os-it-$$"
NETWORK="${PROJECT}_default"
TOKEN="integration_token_at_least_24_chars"
export POSTGRES_PASSWORD="integration_password"
export DATABASE_URL="postgres://agenda_os:integration_password@db:5432/agenda_os?sslmode=disable"
export APP_API_TOKEN="$TOKEN"
export APP_PASSWORD="integration_password"
export SESSION_SECRET="integration_session_secret_at_least_32_chars"
export SESSION_COOKIE_SECURE="false"

cleanup() {
  docker compose -p "$PROJECT" -f "$ROOT_DIR/compose.yaml" down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

fail() {
  echo "falha: $1" >&2
  exit 1
}

api() {
  docker run --rm --network "$NETWORK" curlimages/curl:8.16.0 -fsS \
    -H "Authorization: Bearer $TOKEN" "$@"
}

docker compose -p "$PROJECT" -f "$ROOT_DIR/compose.yaml" up -d --build migrate api worker

operation_id=""
attempt=0
while [ "$attempt" -lt 20 ]; do
  operations=$(api http://api:8080/api/v1/operations 2>/dev/null || true)
  operation_id=$(printf '%s' "$operations" | jq -r '.items[0].id // empty' 2>/dev/null || true)
  [ -n "$operation_id" ] && break
  attempt=$((attempt + 1))
  sleep 1
done
[ -n "$operation_id" ] || fail "API não ficou pronta"

attempt=0
while [ "$attempt" -lt 20 ]; do
  orders=$(api "http://api:8080/api/v1/orders?operation_id=$operation_id" 2>/dev/null || true)
  [ "$(printf '%s' "$orders" | jq -r '.total // 0')" -eq 7 ] && break
  attempt=$((attempt + 1))
  sleep 1
done
[ "$(printf '%s' "$orders" | jq -r '.total // 0')" -eq 7 ] || fail "sincronização simulada não importou sete ordens"

operational_date=$(date +%F)
proposal=$(api -H 'Content-Type: application/json' -d "{\"operation_id\":\"$operation_id\",\"operational_date\":\"$operational_date\"}" http://api:8080/api/v1/planning-proposals)
[ "$(printf '%s' "$proposal" | jq '[.groups[].orders[]]|length')" -eq 4 ] || fail "agrupamento inicial inesperado"
[ "$(printf '%s' "$proposal" | jq '.pending|length')" -eq 1 ] || fail "pendência inicial inesperada"

proposal_id=$(printf '%s' "$proposal" | jq -r '.id')
first_two=$(printf '%s' "$proposal" | jq -c '[.groups[0].orders[0:2][].id]')
remaining=$(printf '%s' "$proposal" | jq -c '[.groups[0].orders[2:][].id]')
pending=$(printf '%s' "$proposal" | jq -c '[.pending[].id]')
update_payload=$(jq -cn --argjson version "$(printf '%s' "$proposal" | jq '.version')" --argjson fixed "$first_two" --argjson remaining "$remaining" --argjson pending "$pending" \
  '{version:$version,team_id:null,agenda_responsible_id:null,groups:[{name:"Grupo preservado",is_fixed:true,order_ids:$fixed},{name:"Grupo recalculável",is_fixed:false,order_ids:$remaining}],pending_order_ids:$pending}')
updated=$(api -X PUT -H 'Content-Type: application/json' -d "$update_payload" "http://api:8080/api/v1/planning-proposals/$proposal_id")

docker compose -p "$PROJECT" -f "$ROOT_DIR/compose.yaml" exec -T db psql -U agenda_os -d agenda_os -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
WITH refs AS (
  SELECT o.id operation_id,ost.service_type_id FROM operations o
  JOIN operation_service_types ost ON ost.operation_id=o.id WHERE o.slug='rural'
), inserted AS (
  INSERT INTO service_orders (source,external_id,operation_id,service_type_id,source_status_code,normalized_status,customer_display,defect_summary,address_original,observed_at)
  SELECT 'simulation','SIM-OS-1006',operation_id,service_type_id,'AB','open','Cliente rural 08','Nova ordem','Estrada Rural Norte, km 14 — área simulada',now() FROM refs
  RETURNING id,address_original
)
INSERT INTO order_locations (order_id,position,source,confidence,address_fingerprint,observed_at)
SELECT id,ST_SetSRID(ST_MakePoint(-46.835,-23.51),4326)::geography,'fixture',1,encode(digest(address_original,'sha256'),'hex'),now() FROM inserted;
SQL

refreshed=$(api -H 'Content-Type: application/json' -d "{\"version\":$(printf '%s' "$updated" | jq '.version')}" "http://api:8080/api/v1/planning-proposals/$proposal_id/refresh")
[ "$(printf '%s' "$refreshed" | jq '[.groups[]|select(.is_fixed and .name=="Grupo preservado")][0].orders|length')" -eq 2 ] || fail "grupo fixado não foi preservado"
[ "$(printf '%s' "$refreshed" | jq '[.groups[].orders[]|select(.external_id=="SIM-OS-1006")]|length')" -eq 1 ] || fail "nova ordem não entrou no recálculo"

orders=$(api "http://api:8080/api/v1/orders?operation_id=$operation_id")
pending_order=$(printf '%s' "$orders" | jq '.items[]|select(.external_id=="SIM-OS-1005")')
pending_id=$(printf '%s' "$pending_order" | jq -r '.id')
location_payload=$(printf '%s' "$pending_order" | jq '{latitude:-23.55,longitude:-46.81,expected_order_version:.observed_version,expected_location_version:.location_version}')
corrected=$(api -X PUT -H 'Content-Type: application/json' -d "$location_payload" "http://api:8080/api/v1/orders/$pending_id/location")
[ "$(printf '%s' "$corrected" | jq -r '.location_source')" = "manual" ] || fail "correção manual não foi persistida"
stale_status=$(docker run --rm --network "$NETWORK" curlimages/curl:8.16.0 -sS -o /dev/null -w '%{http_code}' -X PUT \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "$location_payload" "http://api:8080/api/v1/orders/$pending_id/location")
[ "$stale_status" -eq 409 ] || fail "edição concorrente de localização não retornou 409"

# Simula uma correção feita sobre um endereço que depois é substituído pela
# próxima observação do ERP. A posição manual deve permanecer, mas perder a
# condição de confiança até nova confirmação.
docker compose -p "$PROJECT" -f "$ROOT_DIR/compose.yaml" exec -T db psql -U agenda_os -d agenda_os -v ON_ERROR_STOP=1 \
  -c "UPDATE service_orders SET address_original='Endereço temporariamente alterado' WHERE id='$pending_id';" >/dev/null
second_location_payload=$(printf '%s' "$corrected" | jq '{latitude:.latitude,longitude:.longitude,expected_order_version:.observed_version,expected_location_version:.location_version}')
api -X PUT -H 'Content-Type: application/json' -d "$second_location_payload" "http://api:8080/api/v1/orders/$pending_id/location" >/dev/null
api -H 'Content-Type: application/json' -d "{\"operation_id\":\"$operation_id\"}" http://api:8080/api/v1/sync-runs >/dev/null
attempt=0
while [ "$attempt" -lt 20 ]; do
  changed_order=$(api "http://api:8080/api/v1/orders?operation_id=$operation_id&q=SIM-OS-1005" 2>/dev/null | jq '.items[0]' || true)
  [ "$(printf '%s' "$changed_order" | jq -r '.location_needs_review // false')" = "true" ] && break
  attempt=$((attempt + 1))
  sleep 1
done
[ "$(printf '%s' "$changed_order" | jq -r '.location_needs_review // false')" = "true" ] || fail "mudança de endereço não invalidou a confiança"
[ "$(printf '%s' "$changed_order" | jq -r '.latitude')" = "-23.55" ] || fail "sincronização apagou a posição manual"
confirmation_payload=$(printf '%s' "$changed_order" | jq '{latitude:.latitude,longitude:.longitude,expected_order_version:.observed_version,expected_location_version:.location_version}')
confirmed_location=$(api -X PUT -H 'Content-Type: application/json' -d "$confirmation_payload" "http://api:8080/api/v1/orders/$pending_id/location")
[ "$(printf '%s' "$confirmed_location" | jq -r '.location_needs_review')" = "false" ] || fail "nova confirmação não restaurou a confiança"

refreshed=$(api -H 'Content-Type: application/json' -d "{\"version\":$(printf '%s' "$refreshed" | jq '.version')}" "http://api:8080/api/v1/planning-proposals/$proposal_id/refresh")
[ "$(printf '%s' "$refreshed" | jq '.pending|length')" -eq 0 ] || fail "ordem corrigida permaneceu pendente"

teams=$(api http://api:8080/api/v1/teams)
approval_payload=$(jq -cn --argjson proposal "$refreshed" --arg team "$(printf '%s' "$teams" | jq -r '.items[0].id')" --arg tech "$(printf '%s' "$teams" | jq -r '.items[0].members[0].id')" \
  '{version:$proposal.version,team_id:$team,agenda_responsible_id:$tech,groups:[$proposal.groups[]|{name,is_fixed,order_ids:[.orders[].id]}],pending_order_ids:[$proposal.pending[].id]}')
updated=$(api -X PUT -H 'Content-Type: application/json' -d "$approval_payload" "http://api:8080/api/v1/planning-proposals/$proposal_id")
approved=$(api -H 'Content-Type: application/json' -d "{\"version\":$(printf '%s' "$updated" | jq '.version')}" "http://api:8080/api/v1/planning-proposals/$proposal_id/approve")
[ "$(printf '%s' "$approved" | jq -r '.status')" = "approved" ] || fail "proposta não foi aprovada"

preview=$(api "http://api:8080/api/v1/planning-proposals/$proposal_id/scheduling-preview")
[ "$(printf '%s' "$preview" | jq '.can_create_requests')" = "false" ] || fail "prévia liberou envio indevidamente"
[ "$(printf '%s' "$preview" | jq '.blockers|length')" -ge 3 ] || fail "bloqueios da prévia ausentes"

page=$(api "http://api:8080/api/v1/orders?operation_id=$operation_id&page=1&page_size=2")
[ "$(printf '%s' "$page" | jq '.items|length')" -eq 2 ] || fail "paginação não respeitou page_size"
search=$(api "http://api:8080/api/v1/orders?operation_id=$operation_id&q=SIM-OS-1006")
[ "$(printf '%s' "$search" | jq '.total')" -eq 1 ] || fail "busca paginada não filtrou no banco"

migration_count=$(docker compose -p "$PROJECT" -f "$ROOT_DIR/compose.yaml" exec -T db psql -U agenda_os -d agenda_os -At -c 'SELECT count(*) FROM schema_migrations;')
[ "$migration_count" -eq 6 ] || fail "registro de migrações incompleto"

echo "integração aprovada: migrações, sincronização, planejamento, localização, prévia e paginação"
