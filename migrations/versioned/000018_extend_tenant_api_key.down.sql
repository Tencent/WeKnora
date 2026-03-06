-- Migration 000018 DOWN: Revert tenant api_key column to varchar(64)
ALTER TABLE tenants ALTER COLUMN api_key TYPE varchar(64);
