-- Rollback: 000071_chess_books
DROP INDEX IF EXISTS idx_chess_chapter_revisions_chapter;
DROP TABLE IF EXISTS chess_chapter_revisions;

DROP INDEX IF EXISTS idx_chess_book_images_book;
DROP TABLE IF EXISTS chess_book_images;

DROP INDEX IF EXISTS idx_chess_chapters_title_trgm;
DROP INDEX IF EXISTS idx_chess_chapters_fulltext;
DROP INDEX IF EXISTS idx_chess_chapters_book;
DROP INDEX IF EXISTS idx_chess_chapters_tenant_slug;
DROP TABLE IF EXISTS chess_book_chapters;

DROP INDEX IF EXISTS idx_chess_shelf_books_shelf;
DROP INDEX IF EXISTS idx_chess_shelf_books_book;
DROP TABLE IF EXISTS chess_shelf_books;

DROP INDEX IF EXISTS idx_chess_books_status;
DROP INDEX IF EXISTS idx_chess_books_filter;
DROP INDEX IF EXISTS idx_chess_books_tenant;
DROP INDEX IF EXISTS idx_chess_books_tenant_slug;
DROP TABLE IF EXISTS chess_books;

DROP INDEX IF EXISTS idx_chess_shelves_tenant;
DROP INDEX IF EXISTS idx_chess_shelves_tenant_slug;
DROP TABLE IF EXISTS chess_shelves;
