ALTER TABLE evaluation_model_calls ADD COLUMN pricing_configured INTEGER NOT NULL DEFAULT 0;
ALTER TABLE evaluation_model_calls ADD COLUMN currency TEXT NOT NULL DEFAULT '';
ALTER TABLE evaluation_model_calls ADD COLUMN input_price_per_million REAL NOT NULL DEFAULT 0;
ALTER TABLE evaluation_model_calls ADD COLUMN output_price_per_million REAL NOT NULL DEFAULT 0;
ALTER TABLE evaluation_model_calls ADD COLUMN cache_read_price_per_million REAL NOT NULL DEFAULT 0;
ALTER TABLE evaluation_model_calls ADD COLUMN cache_write_price_per_million REAL NOT NULL DEFAULT 0;
ALTER TABLE evaluation_model_calls ADD COLUMN estimated_cost REAL NOT NULL DEFAULT 0;
