ALTER TABLE memories ADD COLUMN archived_at timestamptz;
ALTER TABLE memories ADD COLUMN pinned boolean NOT NULL DEFAULT false;
ALTER TABLE memories ADD COLUMN utility double precision NOT NULL DEFAULT 0.5;
ALTER TABLE memories ADD COLUMN outcome_count integer NOT NULL DEFAULT 0;
ALTER TABLE consolidations ADD COLUMN resolved_model text NOT NULL DEFAULT '';

-- Evidence time is distinct from the time the model processed it. Historical
-- imports must not acquire a fresh evidence date just by being consolidated.
UPDATE memories m SET last_seen_at = evidence.at
FROM (
    SELECT m.id, max(o.at) AS at FROM memories m
    CROSS JOIN LATERAL jsonb_array_elements_text(m.sources) source(id)
    JOIN observations o ON o.id = source.id AND o.user_id = m.user_id
    GROUP BY m.id
) evidence WHERE m.id = evidence.id;

CREATE TABLE memory_embeddings (
    memory_id text NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    model text NOT NULL,
    content_hash text NOT NULL,
    embedding double precision[] NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (memory_id, model)
);

-- Only one-way hashes are retained when forgetting an observation. Both keys
-- fence repeated imports and exact content copied under a new import identity.
CREATE TABLE forgotten_observations (
    user_id text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('content', 'idempotency')),
    fingerprint text NOT NULL,
    forgotten_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(user_id, kind, fingerprint)
);
CREATE INDEX memories_active_idx ON memories(user_id, last_seen_at DESC)
    WHERE contradicted_by IS NULL AND archived_at IS NULL;
CREATE TABLE forgotten_memories (
    user_id text NOT NULL,
    memory_id text NOT NULL,
    PRIMARY KEY(user_id, memory_id)
);
