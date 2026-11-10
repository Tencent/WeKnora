-- HTTP signatures do not authenticate message bodies.
-- Operators must also enable Stream in the DingTalk developer console.
UPDATE im_channels SET mode = 'websocket', updated_at = CURRENT_TIMESTAMP
WHERE platform = 'dingtalk' AND mode = 'webhook';
