package store

// Per-user wipes behind Settings → Danger zone. Sessions are handled by
// auth.Service.RevokeAll; playlist_items go with their playlist (ON DELETE
// CASCADE), but the explicit delete keeps it correct even with foreign keys off.

func (r *BookmarksRepo) ClearUser(userID int64) error {
	_, err := r.db.Exec(`DELETE FROM bookmarks WHERE user_id = ?`, userID)
	return err
}

func (r *PlaylistsRepo) ClearUser(userID int64) error {
	if _, err := r.db.Exec(`DELETE FROM playlist_items WHERE playlist_id IN (SELECT id FROM playlists WHERE user_id = ?)`, userID); err != nil {
		return err
	}
	_, err := r.db.Exec(`DELETE FROM playlists WHERE user_id = ?`, userID)
	return err
}

func (r *TimecodesRepo) ClearUser(userID int64) error {
	_, err := r.db.Exec(`DELETE FROM timecodes WHERE user_id = ?`, userID)
	return err
}

func (r *SettingsRepo) ClearUser(userID int64) error {
	_, err := r.db.Exec(`DELETE FROM user_settings WHERE user_id = ?`, userID)
	return err
}

// DeleteByUser revokes every session (device) of the user.
func (r *SessionsRepo) DeleteByUser(userID int64) error {
	_, err := r.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}
