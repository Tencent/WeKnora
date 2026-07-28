-- Reassert the defaults introduced by 000010 for installations upgraded from
-- the MySQL compatibility chain.
ALTER TABLE chunks
    ALTER COLUMN seq_id SET DEFAULT nextval('chunks_seq_id_seq');
ALTER TABLE knowledge_tags
    ALTER COLUMN seq_id SET DEFAULT nextval('knowledge_tags_seq_id_seq');
