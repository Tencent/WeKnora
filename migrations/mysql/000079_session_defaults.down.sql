ALTER TABLE sessions
    MODIFY COLUMN fallback_response TEXT NOT NULL
        DEFAULT ('很抱歉，我暂时无法回答这个问题。'),
    MODIFY COLUMN summary_parameters JSON NOT NULL
        DEFAULT (JSON_OBJECT());
