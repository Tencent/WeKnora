ALTER TABLE evaluation_model_calls
    ADD COLUMN request_fingerprint VARCHAR(128) NOT NULL DEFAULT '';
