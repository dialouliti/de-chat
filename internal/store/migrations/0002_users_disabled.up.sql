-- Adds a 'disabled' flag so an admin can block a user without deleting them.
-- Blocked users cannot log in and are rejected on every authenticated request
-- (and active WebSocket connections are denied on reconnect).
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled boolean NOT NULL DEFAULT false;
