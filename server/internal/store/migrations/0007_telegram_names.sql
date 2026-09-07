-- Who is behind each linked Telegram chat, so a profile shared by a family can
-- list and unlink phones individually (Settings → Telegram).
ALTER TABLE telegram_links ADD COLUMN first_name TEXT NOT NULL DEFAULT '';
ALTER TABLE telegram_links ADD COLUMN username   TEXT NOT NULL DEFAULT '';
