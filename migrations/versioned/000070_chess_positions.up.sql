-- Migration: 000070_chess_positions
-- Description: Ngân hàng thế cờ FEN (chess_positions) — thực thể cờ thứ 5 bên
--              cạnh chess_games/chess_puzzles/chess_lessons/chess_courses.
--              Khác chess_puzzles (bài TẬP có lời giải, để LUYỆN), position là
--              thế cờ THAM CHIẾU để DẠY/trích dẫn/phân tích: tàn cuộc lý
--              thuyết, tabiya khai cuộc, mô-típ chiến thuật/cấu trúc tốt, và
--              CỐ Ý cho phép thế cờ giản lược KHÔNG có quân Vua (dạy trẻ mới
--              học). Hỗ trợ wikilink [[position/<slug>]].

DO $$ BEGIN RAISE NOTICE '[Migration 000070] Applying chess_positions schema'; END $$;

CREATE TABLE IF NOT EXISTS chess_positions (
    id             VARCHAR(36) PRIMARY KEY,
    tenant_id      BIGINT NOT NULL,
    slug           VARCHAR(255) NOT NULL DEFAULT '',
    title          VARCHAR(255) NOT NULL DEFAULT '',
    fen            VARCHAR(128) NOT NULL,
    fen_key        VARCHAR(96)  NOT NULL DEFAULT '', -- 4 trường đầu FEN, phát hiện trùng (cảnh báo, không chặn)
    category       VARCHAR(32)  NOT NULL DEFAULT '', -- tabiya|endgame|structure|motif|model|basic
    level          VARCHAR(16)  NOT NULL DEFAULT '', -- tot|ma|tuong|xe|hau|vua (6 cấp Dương Sinh)
    eco            VARCHAR(8)   NOT NULL DEFAULT '', -- nối tabiya với khai cuộc (ECO)
    side_to_move   VARCHAR(1)   NOT NULL DEFAULT '',
    orientation    VARCHAR(1)   NOT NULL DEFAULT '', -- hướng bàn cờ mặc định khi dạy: 'w'|'b'
    assessment     VARCHAR(64)  NOT NULL DEFAULT '', -- nhãn HLV: "Trắng thắng" | "Hòa" | "Trắng ưu"...
    annotation     TEXT         NOT NULL DEFAULT '', -- markdown, ĐƯỢC chứa wikilink [[...]]
    tags           VARCHAR(255) NOT NULL DEFAULT '', -- CSV nhẹ, khớp mẫu theme của chess_puzzles
    source         VARCHAR(255) NOT NULL DEFAULT '',
    source_game_id VARCHAR(36)  NOT NULL DEFAULT '', -- trích từ ván nào (chess_games.id, không FK cứng)
    source_ply     INTEGER      NOT NULL DEFAULT 0,  -- sau nửa-nước thứ mấy
    created_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Duy nhất theo (tenant_id, slug), loại trừ chuỗi rỗng — cùng khuôn mẫu với
-- idx_chess_games_tenant_slug/idx_chess_puzzles_tenant_slug (migration 000064).
CREATE UNIQUE INDEX IF NOT EXISTS idx_chess_positions_tenant_slug
    ON chess_positions (tenant_id, slug) WHERE slug <> '';

CREATE INDEX IF NOT EXISTS idx_chess_positions_tenant
    ON chess_positions (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_chess_positions_filter
    ON chess_positions (tenant_id, category, level);
CREATE INDEX IF NOT EXISTS idx_chess_positions_fenkey
    ON chess_positions (tenant_id, fen_key);
CREATE INDEX IF NOT EXISTS idx_chess_positions_srcgame
    ON chess_positions (tenant_id, source_game_id);
