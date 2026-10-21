ALTER TABLE observations
  ADD COLUMN tool_name     TEXT,
  ADD COLUMN intent_id     TEXT,
  ADD COLUMN outcome_class TEXT,
  ADD COLUMN turn_index    INTEGER;
