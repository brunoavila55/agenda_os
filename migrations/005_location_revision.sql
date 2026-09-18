BEGIN;

ALTER TABLE order_locations
  ADD COLUMN IF NOT EXISTS location_version bigint NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS address_changed boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE proposal_items
  ADD COLUMN IF NOT EXISTS observed_location_version bigint;

UPDATE proposal_items pi
SET observed_location_version = l.location_version
FROM order_locations l
WHERE l.order_id = pi.order_id AND pi.observed_location_version IS NULL;

COMMIT;
