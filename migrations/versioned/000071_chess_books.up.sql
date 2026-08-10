-- Migration: 000071_chess_books
-- Description: Thư viện sách cờ vua — Kệ (chess_shelves) → Sách (chess_books) →
--              Chương (chess_book_chapters), cộng ảnh chèn trong chương
--              (chess_book_images) và lịch sử phiên bản chương
--              (chess_chapter_revisions). Đây là hệ SOẠN THẢO nội bộ (không phải
--              kho ebook PDF/EPUB để đọc) — nội dung chương là markdown có thể
--              chứa FEN/PGN/wikilink, giống bài giảng (chess_lessons) nhưng có
--              thêm lịch sử phiên bản.
--
--              Quan hệ kệ↔sách là NHIỀU-NHIỀU (bảng nối chess_shelf_books) —
--              đúng mô hình bookshelves_books của BookStack: một cuốn sách có
--              thể nằm trên nhiều kệ (chuyên đề/giai đoạn/bộ sách/cấp độ).
--              Chương trong sách là DANH SÁCH 1 CẤP (cột `part` chỉ là nhãn
--              chuỗi để gộp hiển thị theo "Phần", không phải bảng riêng).
--
--              Wikilink: [[book/<slug>]] và [[chapter/<slug>]]. Kệ KHÔNG phải
--              đích wikilink (chỉ điều hướng UI) — xem 04-nhat-ky-tuy-bien.md.

DO $$ BEGIN RAISE NOTICE '[Migration 000071] Applying chess book library schema'; END $$;

CREATE TABLE IF NOT EXISTS chess_shelves (
    id           VARCHAR(36) PRIMARY KEY,
    tenant_id    BIGINT NOT NULL,
    slug         VARCHAR(255) NOT NULL DEFAULT '',
    title        VARCHAR(255) NOT NULL DEFAULT '',
    kind         VARCHAR(16)  NOT NULL DEFAULT '', -- theme|phase|series|level (chuyên đề/giai đoạn/bộ sách/cấp độ)
    description  TEXT         NOT NULL DEFAULT '',
    cover_url    VARCHAR(512) NOT NULL DEFAULT '',
    sort_order   INTEGER      NOT NULL DEFAULT 0,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_chess_shelves_tenant_slug
    ON chess_shelves (tenant_id, slug) WHERE slug <> '';
CREATE INDEX IF NOT EXISTS idx_chess_shelves_tenant
    ON chess_shelves (tenant_id, sort_order);

CREATE TABLE IF NOT EXISTS chess_books (
    id           VARCHAR(36) PRIMARY KEY,
    tenant_id    BIGINT NOT NULL,
    slug         VARCHAR(255) NOT NULL DEFAULT '',
    title        VARCHAR(255) NOT NULL DEFAULT '',
    subtitle     VARCHAR(255) NOT NULL DEFAULT '',
    author       VARCHAR(255) NOT NULL DEFAULT '',
    translator   VARCHAR(255) NOT NULL DEFAULT '',
    publisher    VARCHAR(255) NOT NULL DEFAULT '',
    year         VARCHAR(8)   NOT NULL DEFAULT '',
    isbn         VARCHAR(32)  NOT NULL DEFAULT '',
    language     VARCHAR(16)  NOT NULL DEFAULT '',
    level        VARCHAR(16)  NOT NULL DEFAULT '', -- tot|ma|tuong|xe|hau|vua (6 cấp Dương Sinh)
    phase        VARCHAR(16)  NOT NULL DEFAULT '', -- opening|middlegame|endgame|tactics|rules|general
    eco          VARCHAR(8)   NOT NULL DEFAULT '',
    status       VARCHAR(16)  NOT NULL DEFAULT 'draft', -- draft|published — chỉ published được index RAG
    description  TEXT         NOT NULL DEFAULT '',
    cover_url    VARCHAR(512) NOT NULL DEFAULT '',
    tags         VARCHAR(255) NOT NULL DEFAULT '',
    sort_order   INTEGER      NOT NULL DEFAULT 0,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_chess_books_tenant_slug
    ON chess_books (tenant_id, slug) WHERE slug <> '';
CREATE INDEX IF NOT EXISTS idx_chess_books_tenant
    ON chess_books (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_chess_books_filter
    ON chess_books (tenant_id, level, phase);
CREATE INDEX IF NOT EXISTS idx_chess_books_status
    ON chess_books (tenant_id, status);

-- Bảng nối NHIỀU-NHIỀU kệ↔sách (mẫu bookshelves_books của BookStack). Không FK
-- cứng (khớp quy ước cờ) — xóa sách/kệ dọn hàng trong bảng này ở tầng service.
CREATE TABLE IF NOT EXISTS chess_shelf_books (
    tenant_id    BIGINT NOT NULL,
    shelf_id     VARCHAR(36) NOT NULL,
    book_id      VARCHAR(36) NOT NULL,
    sort_order   INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (shelf_id, book_id)
);

CREATE INDEX IF NOT EXISTS idx_chess_shelf_books_book
    ON chess_shelf_books (tenant_id, book_id);
CREATE INDEX IF NOT EXISTS idx_chess_shelf_books_shelf
    ON chess_shelf_books (tenant_id, shelf_id, sort_order);

CREATE TABLE IF NOT EXISTS chess_book_chapters (
    id           VARCHAR(36) PRIMARY KEY,
    tenant_id    BIGINT NOT NULL,
    slug         VARCHAR(255) NOT NULL DEFAULT '',
    book_id      VARCHAR(36)  NOT NULL DEFAULT '',
    part         VARCHAR(255) NOT NULL DEFAULT '', -- nhãn "Phần" để gộp hiển thị (không phải bảng riêng)
    title        VARCHAR(255) NOT NULL DEFAULT '',
    content      TEXT         NOT NULL DEFAULT '', -- markdown, CÓ THỂ chứa wikilink + khối ```chess
    fen          VARCHAR(128) NOT NULL DEFAULT '', -- thế cờ minh họa chính của chương (tùy chọn)
    level        VARCHAR(16)  NOT NULL DEFAULT '',
    sort_order   INTEGER      NOT NULL DEFAULT 0,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_chess_chapters_tenant_slug
    ON chess_book_chapters (tenant_id, slug) WHERE slug <> '';
CREATE INDEX IF NOT EXISTS idx_chess_chapters_book
    ON chess_book_chapters (tenant_id, book_id, sort_order);
-- Full-text + trigram cho tìm kiếm nội dung chương (mẫu wiki_pages, migration
-- 000037/000041). pg_trgm đã cài ở migration 000002.
CREATE INDEX IF NOT EXISTS idx_chess_chapters_fulltext
    ON chess_book_chapters USING GIN (to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, '')));
CREATE INDEX IF NOT EXISTS idx_chess_chapters_title_trgm
    ON chess_book_chapters USING GIN (lower(title) gin_trgm_ops);

-- Ảnh chèn trong nội dung chương (upload qua POST /chess/books/:id/images).
-- `path` là provider:// path trả về từ FileService (local/minio/cos/...).
CREATE TABLE IF NOT EXISTS chess_book_images (
    id           VARCHAR(36) PRIMARY KEY,
    tenant_id    BIGINT NOT NULL,
    book_id      VARCHAR(36)  NOT NULL DEFAULT '',
    path         VARCHAR(512) NOT NULL DEFAULT '',
    file_name    VARCHAR(255) NOT NULL DEFAULT '',
    mime         VARCHAR(64)  NOT NULL DEFAULT '',
    size         BIGINT       NOT NULL DEFAULT 0,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_chess_book_images_book
    ON chess_book_images (tenant_id, book_id);

-- Lịch sử phiên bản chương: lưu bản TRƯỚC khi ghi đè (pre-image), chỉ khi nội
-- dung/tiêu đề thực sự đổi (không lưu khi sửa các trường khác như sort_order).
CREATE TABLE IF NOT EXISTS chess_chapter_revisions (
    id               VARCHAR(36) PRIMARY KEY,
    tenant_id        BIGINT NOT NULL,
    chapter_id       VARCHAR(36)  NOT NULL DEFAULT '',
    revision_number  INTEGER      NOT NULL DEFAULT 0,
    title            VARCHAR(255) NOT NULL DEFAULT '',
    content          TEXT         NOT NULL DEFAULT '',
    summary          VARCHAR(255) NOT NULL DEFAULT '', -- ghi chú thay đổi (tùy chọn)
    created_by       VARCHAR(64)  NOT NULL DEFAULT '',
    created_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_chess_chapter_revisions_chapter
    ON chess_chapter_revisions (tenant_id, chapter_id, revision_number DESC);
