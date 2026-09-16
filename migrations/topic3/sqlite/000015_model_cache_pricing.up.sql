ALTER TABLE model_price_versions ADD COLUMN cache_pricing TEXT NULL CHECK (cache_pricing IS NULL OR json_valid(cache_pricing));
