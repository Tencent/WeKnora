DO \$\$ BEGIN RAISE NOTICE '[Migration 000066] Extending dislike_reason column length'; END \$\$;
ALTER TABLE user_message_feedbacks ALTER COLUMN dislike_reason TYPE VARCHAR(500);
