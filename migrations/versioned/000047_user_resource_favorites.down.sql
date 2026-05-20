-- Reverse of 000047_user_resource_favorites.
DO $$ BEGIN RAISE NOTICE '[Migration 000047] Dropping table: user_resource_favorites'; END $$;

DROP INDEX IF EXISTS idx_user_resource_favorites_tenant_id;
DROP INDEX IF EXISTS idx_user_resource_favorites_user_tenant_type_created_at;
DROP TABLE IF EXISTS user_resource_favorites;

DO $$ BEGIN RAISE NOTICE '[Migration 000047] user_resource_favorites table dropped'; END $$;
