BEGIN;

CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE normalized_order_status AS ENUM ('open', 'scheduled', 'closed', 'cancelled', 'unknown');
CREATE TYPE job_status AS ENUM ('pending', 'running', 'completed', 'failed');
CREATE TYPE proposal_status AS ENUM ('draft', 'approved', 'superseded', 'conflicted');
CREATE TYPE scheduling_status AS ENUM ('pending', 'sending', 'confirmed', 'failed', 'uncertain');

CREATE TABLE operations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  slug text NOT NULL UNIQUE,
  name text NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  sync_interval_seconds integer NOT NULL DEFAULT 600 CHECK (sync_interval_seconds >= 60),
  timezone text NOT NULL,
  grouping_radius_meters integer NOT NULL DEFAULT 10000 CHECK (grouping_radius_meters > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mk_service_types (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  source text NOT NULL,
  external_id text NOT NULL,
  description text NOT NULL,
  observed_at timestamptz NOT NULL,
  UNIQUE (source, external_id)
);

CREATE TABLE operation_service_types (
  operation_id uuid NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
  service_type_id uuid NOT NULL REFERENCES mk_service_types(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (operation_id, service_type_id),
  UNIQUE (service_type_id)
);

CREATE TABLE teams (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  source text NOT NULL,
  external_id text NOT NULL,
  name text NOT NULL,
  observed_at timestamptz NOT NULL,
  UNIQUE (source, external_id)
);

CREATE TABLE technicians (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  source text NOT NULL,
  external_id text NOT NULL,
  name text NOT NULL,
  observed_at timestamptz NOT NULL,
  UNIQUE (source, external_id)
);

CREATE TABLE team_members (
  team_id uuid NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  technician_id uuid NOT NULL REFERENCES technicians(id) ON DELETE CASCADE,
  PRIMARY KEY (team_id, technician_id)
);

CREATE TABLE service_orders (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  source text NOT NULL,
  external_id text NOT NULL,
  operation_id uuid REFERENCES operations(id) ON DELETE RESTRICT,
  service_type_id uuid REFERENCES mk_service_types(id) ON DELETE RESTRICT,
  source_status_code text NOT NULL,
  normalized_status normalized_order_status NOT NULL DEFAULT 'unknown',
  scheduled_at timestamptz,
  responsible_external_id text,
  customer_display text NOT NULL,
  defect_summary text,
  address_original text NOT NULL,
  observed_version bigint NOT NULL DEFAULT 1,
  observed_at timestamptz NOT NULL,
  first_observed_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (source, external_id)
);

CREATE TABLE order_locations (
  order_id uuid PRIMARY KEY REFERENCES service_orders(id) ON DELETE CASCADE,
  position geography(Point, 4326),
  source text NOT NULL,
  confidence numeric(4,3) CHECK (confidence BETWEEN 0 AND 1),
  reviewed_manually boolean NOT NULL DEFAULT false,
  address_fingerprint text NOT NULL,
  observed_at timestamptz NOT NULL,
  CHECK (position IS NULL OR ST_IsValid(position::geometry))
);

CREATE INDEX service_orders_filter_idx ON service_orders (operation_id, normalized_status, scheduled_at);
CREATE INDEX order_locations_gix ON order_locations USING gist (position);

CREATE TABLE planning_proposals (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  operation_id uuid NOT NULL REFERENCES operations(id) ON DELETE RESTRICT,
  operational_date date NOT NULL,
  status proposal_status NOT NULL DEFAULT 'draft',
  version integer NOT NULL DEFAULT 1,
  team_id uuid REFERENCES teams(id) ON DELETE RESTRICT,
  agenda_responsible_id uuid REFERENCES technicians(id) ON DELETE RESTRICT,
  generator text NOT NULL DEFAULT 'system',
  approved_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX one_active_proposal_per_day
  ON planning_proposals (operation_id, operational_date)
  WHERE status IN ('draft', 'approved', 'conflicted');

CREATE TABLE proposal_groups (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  proposal_id uuid NOT NULL REFERENCES planning_proposals(id) ON DELETE CASCADE,
  name text NOT NULL,
  position integer NOT NULL,
  UNIQUE (proposal_id, position)
);

CREATE TABLE proposal_items (
  proposal_id uuid NOT NULL REFERENCES planning_proposals(id) ON DELETE CASCADE,
  order_id uuid NOT NULL REFERENCES service_orders(id) ON DELETE RESTRICT,
  group_id uuid REFERENCES proposal_groups(id) ON DELETE CASCADE,
  is_pending boolean NOT NULL DEFAULT false,
  observed_order_version bigint NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (proposal_id, order_id),
  CHECK ((group_id IS NOT NULL) <> is_pending)
);

CREATE TABLE scheduling_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  proposal_id uuid NOT NULL REFERENCES planning_proposals(id) ON DELETE RESTRICT,
  order_id uuid NOT NULL REFERENCES service_orders(id) ON DELETE RESTRICT,
  approved_proposal_version integer NOT NULL,
  status scheduling_status NOT NULL DEFAULT 'pending',
  starts_at timestamptz NOT NULL,
  ends_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (ends_at > starts_at)
);

CREATE UNIQUE INDEX one_active_scheduling_command
  ON scheduling_requests (order_id)
  WHERE status IN ('pending', 'sending', 'uncertain');

CREATE TABLE scheduling_attempts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  request_id uuid NOT NULL REFERENCES scheduling_requests(id) ON DELETE CASCADE,
  attempt_number integer NOT NULL,
  outcome text NOT NULL,
  sanitized_error text,
  started_at timestamptz NOT NULL,
  finished_at timestamptz,
  UNIQUE (request_id, attempt_number)
);

CREATE TABLE sync_runs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  operation_id uuid NOT NULL REFERENCES operations(id) ON DELETE RESTRICT,
  source text NOT NULL,
  trigger text NOT NULL,
  status text NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'partial', 'failed')),
  orders_seen integer NOT NULL DEFAULT 0,
  started_at timestamptz,
  finished_at timestamptz,
  error_category text,
  sanitized_error text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE jobs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  kind text NOT NULL,
  operation_id uuid REFERENCES operations(id) ON DELETE CASCADE,
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  status job_status NOT NULL DEFAULT 'pending',
  attempts integer NOT NULL DEFAULT 0,
  max_attempts integer NOT NULL DEFAULT 5,
  run_after timestamptz NOT NULL DEFAULT now(),
  lease_until timestamptz,
  worker_id text,
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX jobs_claim_idx ON jobs (status, run_after, lease_until);
CREATE UNIQUE INDEX one_sync_job_per_operation
  ON jobs (kind, operation_id)
  WHERE kind = 'sync_operation' AND status IN ('pending', 'running');

CREATE TABLE events (
  id bigserial PRIMARY KEY,
  aggregate_type text NOT NULL,
  aggregate_id uuid NOT NULL,
  event_type text NOT NULL,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO operations (slug, name, timezone, grouping_radius_meters)
VALUES ('rural', 'Manutenção rural', 'America/Sao_Paulo', 12000);

INSERT INTO mk_service_types (source, external_id, description, observed_at)
VALUES
  ('simulation', 'SIM-RURAL-01', 'Manutenção rural — exemplo', now()),
  ('simulation', 'SIM-FIBRA-01', 'Manutenção de fibra — exemplo', now());

INSERT INTO operation_service_types (operation_id, service_type_id)
SELECT o.id, t.id FROM operations o, mk_service_types t
WHERE o.slug = 'rural' AND t.external_id = 'SIM-RURAL-01';

INSERT INTO teams (source, external_id, name, observed_at)
VALUES ('simulation', 'SIM-EQUIPE-RURAL', 'Equipe rural', now());

INSERT INTO technicians (source, external_id, name, observed_at)
VALUES
  ('simulation', 'SIM-TEC-01', 'Técnico A (simulado)', now()),
  ('simulation', 'SIM-TEC-02', 'Técnico B (simulado)', now());

INSERT INTO team_members (team_id, technician_id)
SELECT e.id, t.id FROM teams e CROSS JOIN technicians t
WHERE e.external_id = 'SIM-EQUIPE-RURAL';

COMMIT;
