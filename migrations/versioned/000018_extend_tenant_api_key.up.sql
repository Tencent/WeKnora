-- Migration 000018: Extend tenant api_key column to support encrypted values
ALTER TABLE tenants ALTER COLUMN api_key TYPE varchar(256);
