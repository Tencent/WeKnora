-- Rollback: 000070_chess_positions
DROP INDEX IF EXISTS idx_chess_positions_srcgame;
DROP INDEX IF EXISTS idx_chess_positions_fenkey;
DROP INDEX IF EXISTS idx_chess_positions_filter;
DROP INDEX IF EXISTS idx_chess_positions_tenant;
DROP INDEX IF EXISTS idx_chess_positions_tenant_slug;
DROP TABLE IF EXISTS chess_positions;
