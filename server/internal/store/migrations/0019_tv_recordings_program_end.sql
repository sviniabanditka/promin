-- The programme's own end time, unpadded: the identity of "this programme is
-- already scheduled" for the guide's toggle and its ⏺ marker. end_at stays the
-- padded time the recorder stops at.
ALTER TABLE tv_recordings ADD COLUMN program_end INTEGER NOT NULL DEFAULT 0;
