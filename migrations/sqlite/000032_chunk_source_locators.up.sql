-- Positions of each chunk in its original file (see versioned/000112).
ALTER TABLE chunks ADD COLUMN source_locators TEXT;
