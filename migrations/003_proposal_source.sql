BEGIN;

ALTER TABLE planning_proposals ADD COLUMN IF NOT EXISTS source text;
UPDATE planning_proposals SET source = 'simulation' WHERE source IS NULL;
ALTER TABLE planning_proposals ALTER COLUMN source SET NOT NULL;

DROP INDEX IF EXISTS one_active_proposal_per_day;
CREATE UNIQUE INDEX one_active_proposal_per_day
  ON planning_proposals (source, operation_id, operational_date)
  WHERE status IN ('draft', 'approved', 'conflicted');

COMMIT;
