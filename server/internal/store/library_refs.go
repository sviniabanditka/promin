package store

type LibraryRef struct {
	TMDBID    int64
	MediaType string
}

// LibraryRefs returns the distinct titles across ALL profiles' bookmarks
// and timecodes, most-recently-touched first. Used by the
// background prewarm job to make sure the household's own library is always
// present locally (fast, and unaffected by a TMDB outage).
func (db *DB) LibraryRefs(limit int) ([]LibraryRef, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := db.SQL.Query(
		`SELECT tmdb_id, media_type, MAX(touched) AS touched FROM (
		     SELECT tmdb_id, media_type, added_at   AS touched FROM bookmarks
		     UNION ALL
		     SELECT tmdb_id, media_type, updated_at AS touched FROM timecodes
		 )
		 GROUP BY tmdb_id, media_type
		 ORDER BY touched DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LibraryRef
	for rows.Next() {
		var ref LibraryRef
		var touched int64
		if err := rows.Scan(&ref.TMDBID, &ref.MediaType, &touched); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}
