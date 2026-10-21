CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE observations (
  id              TEXT PRIMARY KEY,
  user_id         TEXT NOT NULL,
  source          TEXT NOT NULL,
  content         TEXT NOT NULL,
  refs            JSONB NOT NULL DEFAULT '{}'::jsonb,
  salience        DOUBLE PRECISION NOT NULL DEFAULT 0.5,
  at              TIMESTAMPTZ NOT NULL DEFAULT now(),
  consolidated_at TIMESTAMPTZ,
  idempotency_key TEXT
);
CREATE UNIQUE INDEX observations_idem_idx ON observations (idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX observations_unconsolidated_idx ON observations (user_id, at) WHERE consolidated_at IS NULL;

CREATE TABLE memories (
  id               TEXT PRIMARY KEY,
  user_id          TEXT NOT NULL,
  kind             TEXT NOT NULL,
  summary          TEXT NOT NULL,
  confidence       DOUBLE PRECISION NOT NULL,
  reinforced_count INTEGER NOT NULL DEFAULT 1,
  born_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  contradicted_by  TEXT,
  expires_at       TIMESTAMPTZ,
  sources          JSONB NOT NULL DEFAULT '[]'::jsonb
);
CREATE INDEX memories_user_conf_idx ON memories (user_id, confidence DESC, last_seen_at DESC);
CREATE INDEX memories_summary_trgm_idx ON memories USING gin (summary gin_trgm_ops);

CREATE TABLE memory_edges (
  source_id TEXT NOT NULL,
  target_id TEXT NOT NULL,
  edge_type TEXT NOT NULL,
  weight    DOUBLE PRECISION NOT NULL DEFAULT 0.5,
  metadata  JSONB NOT NULL DEFAULT '{}'::jsonb,
  PRIMARY KEY (source_id, target_id, edge_type)
);

CREATE TABLE dream_sessions (
  id                     TEXT PRIMARY KEY,
  user_id                TEXT NOT NULL,
  status                 TEXT NOT NULL DEFAULT 'submitted',
  anthropic_batch_id     TEXT,
  batch_key              TEXT NOT NULL,
  started_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at           TIMESTAMPTZ,
  observations_processed INTEGER NOT NULL DEFAULT 0,
  memories_created       INTEGER NOT NULL DEFAULT 0,
  memories_reinforced    INTEGER NOT NULL DEFAULT 0,
  memories_decayed       INTEGER NOT NULL DEFAULT 0,
  tokens_used            INTEGER NOT NULL DEFAULT 0,
  error_message          TEXT
);
CREATE UNIQUE INDEX dream_sessions_active_idx ON dream_sessions (user_id) WHERE status IN ('submitted','in_progress');
CREATE UNIQUE INDEX dream_sessions_key_idx ON dream_sessions (batch_key);

CREATE TABLE training_events (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL,
  memory_id    TEXT,
  signal_type  TEXT NOT NULL CHECK (signal_type IN ('retrieved','used','ignored','reinforced','decayed','contradicted')),
  signal_value DOUBLE PRECISION NOT NULL DEFAULT 0,
  metadata     JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX training_events_user_idx ON training_events (user_id, created_at DESC);

-- schema_version is created by the runner before applying any migration; the
-- runner records each successful migration's version atomically with its body.
