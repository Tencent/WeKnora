ALTER TABLE evaluation_model_calls
    ADD COLUMN request_fingerprint TEXT NOT NULL DEFAULT '';
