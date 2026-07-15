-- Message editing: records when (and that) a message was last edited.
-- edited_at is NULL until the message is edited for the first time.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS edited_at timestamptz;

-- Per-room, per-user read cursor. The last time a user marked a room as read.
-- Senders derive "read by N" from this by counting rows whose last_read_at is
-- on or after a given message's created_at.
CREATE TABLE IF NOT EXISTS message_read_state (
    room_id     uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    last_read_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, user_id)
);
