-- Emoji reactions on messages. One row per (message, user, emoji): re-sending
-- the same triple is a PK conflict, which the store's toggle path turns into a
-- DELETE. Rows cascade away with the message or the reacting user.
CREATE TABLE IF NOT EXISTS message_reactions (
    message_id uuid NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji      text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (message_id, user_id, emoji)
);
