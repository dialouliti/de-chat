-- Per-room roles: moderation-only. Visibility stays "all users see all rooms";
-- this table records who owns each room so owners (and global admins) can
-- clear/delete their rooms and manage members.
CREATE TABLE IF NOT EXISTS room_members (
    room_id  uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    user_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role     text NOT NULL DEFAULT 'member',
    joined_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, user_id),
    CONSTRAINT room_members_role_check CHECK (role IN ('owner', 'member'))
);

-- Backfill: make each room's creator the owner, so existing rooms have an
-- owner without requiring manual setup. ON CONFLICT covers rooms already
-- present in the table.
INSERT INTO room_members (room_id, user_id, role)
SELECT id, created_by, 'owner' FROM rooms
ON CONFLICT (room_id, user_id) DO NOTHING;
