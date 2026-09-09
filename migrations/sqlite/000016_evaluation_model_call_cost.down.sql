ALTER TABLE evaluation_model_calls DROP COLUMN estimated_cost;
ALTER TABLE evaluation_model_calls DROP COLUMN cache_write_price_per_million;
ALTER TABLE evaluation_model_calls DROP COLUMN cache_read_price_per_million;
ALTER TABLE evaluation_model_calls DROP COLUMN output_price_per_million;
ALTER TABLE evaluation_model_calls DROP COLUMN input_price_per_million;
ALTER TABLE evaluation_model_calls DROP COLUMN currency;
ALTER TABLE evaluation_model_calls DROP COLUMN pricing_configured;
