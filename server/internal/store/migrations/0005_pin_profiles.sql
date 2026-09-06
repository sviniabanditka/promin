-- PIN-based profiles: each user gains an optional deterministic PIN lookup hash
-- (hex(HMAC-SHA256(pinSecret, pin))). A partial-unique index enforces that a
-- 6-digit PIN maps to exactly one profile. NULL = no PIN (profile not enterable
-- by PIN — the safe closed-by-default state; also the admin, user 1).
ALTER TABLE users ADD COLUMN pin_lookup TEXT;
CREATE UNIQUE INDEX idx_users_pin ON users(pin_lookup) WHERE pin_lookup IS NOT NULL;
