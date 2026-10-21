-- Retire only legacy in-flight jobs. Their observations remain unacknowledged;
-- historical completed/failed sessions, observations, and memories are retained.
UPDATE dream_sessions SET status='failed', completed_at=now(),
  error_message='Retired legacy batch workflow; unprocessed observations may be consolidated again'
WHERE status IN ('submitted', 'in_progress');

DROP INDEX observations_idem_idx;
CREATE UNIQUE INDEX observations_idem_idx ON observations (user_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL;

CREATE TABLE consolidations (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed')),
  observation_ids TEXT[] NOT NULL CHECK (cardinality(observation_ids) > 0),
  model TEXT NOT NULL,
  prompt_version TEXT NOT NULL,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  observations_processed INTEGER NOT NULL DEFAULT 0,
  memories_created INTEGER NOT NULL DEFAULT 0,
  memories_reinforced INTEGER NOT NULL DEFAULT 0,
  tokens_used INTEGER NOT NULL DEFAULT 0,
  error_message TEXT
);
CREATE UNIQUE INDEX consolidations_running_idx ON consolidations(user_id) WHERE status='running';
CREATE INDEX consolidations_user_idx ON consolidations(user_id, started_at DESC);
