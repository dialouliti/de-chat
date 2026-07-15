DROP TABLE IF EXISTS message_read_state;
ALTER TABLE messages DROP COLUMN IF EXISTS edited_at;
