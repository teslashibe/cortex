CREATE TABLE observation_embeddings (
    observation_id text NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    model text NOT NULL,
    content_hash text NOT NULL,
    embedding double precision[] NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (observation_id, model)
);
CREATE INDEX memories_sources_idx ON memories USING gin(sources);
