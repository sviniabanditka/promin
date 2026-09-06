package store

import (
	"database/sql"
	"errors"
)

// Playlist is a row of the playlists table.
type Playlist struct {
	ID        int64
	UserID    int64
	Name      string
	CreatedAt int64
	UpdatedAt int64
}

// PlaylistItem is a row of the playlist_items table.
type PlaylistItem struct {
	ID         int64
	PlaylistID int64
	TMDBID     int64
	MediaType  string
	Position   int
	AddedAt    int64
}

// PlaylistsRepo is the repository over playlists/playlist_items.
type PlaylistsRepo struct {
	db *sql.DB
}

// List returns userID's playlists, most recently updated first.
func (r *PlaylistsRepo) List(userID int64) ([]Playlist, error) {
	rows, err := r.db.Query(
		`SELECT id, user_id, name, created_at, updated_at FROM playlists
		 WHERE user_id = ? ORDER BY updated_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Playlist
	for rows.Next() {
		var p Playlist
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ItemsCount returns the number of items in playlistID (for the list
// response's items_count field, docs/api.md).
func (r *PlaylistsRepo) ItemsCount(playlistID int64) (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM playlist_items WHERE playlist_id = ?`, playlistID).Scan(&n)
	return n, err
}

// Get returns the playlist with id, scoped to userID, or ErrNotFound.
func (r *PlaylistsRepo) Get(userID, id int64) (Playlist, error) {
	var p Playlist
	err := r.db.QueryRow(
		`SELECT id, user_id, name, created_at, updated_at FROM playlists WHERE id = ? AND user_id = ?`,
		id, userID,
	).Scan(&p.ID, &p.UserID, &p.Name, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Playlist{}, ErrNotFound
	}
	return p, err
}

// Create inserts a new playlist.
func (r *PlaylistsRepo) Create(userID int64, name string, now int64) (Playlist, error) {
	res, err := r.db.Exec(
		`INSERT INTO playlists (user_id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		userID, name, now, now,
	)
	if err != nil {
		return Playlist{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Playlist{}, err
	}
	return Playlist{ID: id, UserID: userID, Name: name, CreatedAt: now, UpdatedAt: now}, nil
}

// Rename updates a playlist's name and updated_at, scoped to userID.
// Returns ErrNotFound if no row matched.
func (r *PlaylistsRepo) Rename(userID, id int64, name string, now int64) error {
	res, err := r.db.Exec(
		`UPDATE playlists SET name = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		name, now, id, userID,
	)
	if err != nil {
		return err
	}
	return checkRowsAffected(res)
}

// Delete removes a playlist (cascades to playlist_items), scoped to userID.
func (r *PlaylistsRepo) Delete(userID, id int64) error {
	res, err := r.db.Exec(`DELETE FROM playlists WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	return checkRowsAffected(res)
}

// Items returns the items of playlistID, ordered by manual position.
func (r *PlaylistsRepo) Items(playlistID int64) ([]PlaylistItem, error) {
	rows, err := r.db.Query(
		`SELECT id, playlist_id, tmdb_id, media_type, position, added_at
		 FROM playlist_items WHERE playlist_id = ? ORDER BY position, id`,
		playlistID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PlaylistItem
	for rows.Next() {
		var it PlaylistItem
		if err := rows.Scan(&it.ID, &it.PlaylistID, &it.TMDBID, &it.MediaType, &it.Position, &it.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// AddItem appends an item to the end of the playlist (position =
// current max + 1). Idempotent: re-adding the same (playlist, tmdb_id,
// media_type) is a no-op update of added_at, per the UNIQUE constraint.
func (r *PlaylistsRepo) AddItem(playlistID, tmdbID int64, mediaType string, now int64) (PlaylistItem, error) {
	var maxPos sql.NullInt64
	if err := r.db.QueryRow(`SELECT MAX(position) FROM playlist_items WHERE playlist_id = ?`, playlistID).Scan(&maxPos); err != nil {
		return PlaylistItem{}, err
	}
	pos := 0
	if maxPos.Valid {
		pos = int(maxPos.Int64) + 1
	}

	res, err := r.db.Exec(
		`INSERT INTO playlist_items (playlist_id, tmdb_id, media_type, position, added_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (playlist_id, tmdb_id, media_type) DO UPDATE SET added_at = excluded.added_at`,
		playlistID, tmdbID, mediaType, pos, now,
	)
	if err != nil {
		return PlaylistItem{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return PlaylistItem{}, err
	}
	return PlaylistItem{ID: id, PlaylistID: playlistID, TMDBID: tmdbID, MediaType: mediaType, Position: pos, AddedAt: now}, nil
}

// RemoveItem deletes a playlist item, scoped to playlistID.
func (r *PlaylistsRepo) RemoveItem(playlistID, itemID int64) error {
	res, err := r.db.Exec(`DELETE FROM playlist_items WHERE id = ? AND playlist_id = ?`, itemID, playlistID)
	if err != nil {
		return err
	}
	return checkRowsAffected(res)
}

func checkRowsAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
