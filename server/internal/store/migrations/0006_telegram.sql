-- Telegram companion bot: one private chat is linked to one profile. The
-- chat id is the key (a chat belongs to exactly one profile); a profile may
-- have several chats. Deleting the profile drops its links.
CREATE TABLE telegram_links (
    chat_id    INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_telegram_links_user ON telegram_links(user_id);
