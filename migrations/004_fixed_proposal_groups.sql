BEGIN;

ALTER TABLE proposal_groups
  ADD COLUMN IF NOT EXISTS is_fixed boolean NOT NULL DEFAULT false;

COMMIT;
