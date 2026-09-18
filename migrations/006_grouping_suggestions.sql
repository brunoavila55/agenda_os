BEGIN;

CREATE TABLE IF NOT EXISTS grouping_suggestions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  proposal_id uuid NOT NULL REFERENCES planning_proposals(id) ON DELETE CASCADE,
  proposal_version integer NOT NULL,
  provider text NOT NULL,
  model text NOT NULL,
  prompt_version text NOT NULL,
  input_signature text NOT NULL,
  status text NOT NULL CHECK (status IN ('validated', 'rejected', 'failed')),
  validated_result jsonb,
  sanitized_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((status = 'validated') = (validated_result IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS grouping_suggestions_proposal_idx
  ON grouping_suggestions (proposal_id, created_at DESC);

COMMIT;
