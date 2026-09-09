ALTER TABLE evaluation_model_calls
    DROP COLUMN IF EXISTS estimated_cost,
    DROP COLUMN IF EXISTS cache_write_price_per_million,
    DROP COLUMN IF EXISTS cache_read_price_per_million,
    DROP COLUMN IF EXISTS output_price_per_million,
    DROP COLUMN IF EXISTS input_price_per_million,
    DROP COLUMN IF EXISTS currency,
    DROP COLUMN IF EXISTS pricing_configured;
