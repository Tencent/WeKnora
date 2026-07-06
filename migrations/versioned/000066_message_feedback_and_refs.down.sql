-- Reverse migration for 000066_message_feedback_and_refs

DROP TABLE IF EXISTS message_chunk_refs;
DROP TABLE IF EXISTS message_feedback;
