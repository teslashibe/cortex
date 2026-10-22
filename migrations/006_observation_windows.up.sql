CREATE TABLE observation_window_embeddings (
    observation_id text NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    model text NOT NULL,
    content_hash text NOT NULL,
    start_rune integer NOT NULL CHECK (start_rune >= 0),
    end_rune integer NOT NULL CHECK (end_rune > start_rune),
    embedding double precision[] NOT NULL,
    PRIMARY KEY (observation_id, model, start_rune)
);
