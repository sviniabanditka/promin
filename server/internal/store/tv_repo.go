package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// TVChannel is one row of tv_channels plus what the list needs.
type TVChannel struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Country    string   `json:"country"`
	Categories []string `json:"categories"`
	Logo       string   `json:"logo"`
	Website    string   `json:"website,omitempty"`
	Network    string   `json:"network,omitempty"`
	Quality    string   `json:"quality"` // best alive stream's quality
	Streams    int      `json:"streams"` // alive streams
	Favorite   bool     `json:"favorite"`
	AltNames   []string `json:"-"` // iptv-org alt_names, for EPG matching only
}

// TVChannelName is what the EPG matcher needs.
type TVChannelName struct {
	ID       string
	Name     string
	Country  string
	AltNames []string
}

// TVProgram is one guide entry (unix seconds).
type TVProgram struct {
	ChannelID string `json:"-"`
	Start     int64  `json:"start"`
	Stop      int64  `json:"stop"`
	Title     string `json:"title"`
	Desc      string `json:"desc,omitempty"`
}

// TVNowNext is the current and following programme of a channel.
type TVNowNext struct {
	Now  *TVProgram `json:"now,omitempty"`
	Next *TVProgram `json:"next,omitempty"`
}

// TVStream is one playable URL of a channel.
type TVStream struct {
	ID        int64
	ChannelID string
	URL       string
	Quality   string
	UserAgent string
	Referrer  string
	Alive     bool
	Fails     int
	CORS      bool // upstream sends Access-Control-Allow-Origin: * (direct play possible)
}

type TVRepo struct {
	db *sql.DB
}

// Replace swaps the whole catalogue in one transaction. Streams keep their
// liveness verdict when the same (channel, url) still exists.
func (r *TVRepo) Replace(channels []TVChannel, streams []TVStream) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	// Remember verdicts, then rebuild.
	type verdict struct {
		alive     bool
		cors      bool
		fails     int
		checkedAt int64
	}
	old := map[string]verdict{}
	rows, err := tx.Query(`SELECT channel_id, url, alive, cors, fails, checked_at FROM tv_streams`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var ch, u string
		var v verdict
		var alive, cors int
		if err := rows.Scan(&ch, &u, &alive, &cors, &v.fails, &v.checkedAt); err != nil {
			rows.Close()
			return err
		}
		v.alive = alive == 1
		v.cors = cors == 1
		old[ch+"\n"+u] = v
	}
	rows.Close()
	if _, err := tx.Exec(`DELETE FROM tv_streams`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tv_channels`); err != nil {
		return err
	}
	chStmt, err := tx.Prepare(`INSERT INTO tv_channels(id, name, country, categories, logo, website, network, alt_names, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer chStmt.Close()
	for _, c := range channels {
		cats, _ := json.Marshal(c.Categories)
		alt, _ := json.Marshal(c.AltNames)
		if c.AltNames == nil {
			alt = []byte("[]")
		}
		if _, err := chStmt.Exec(c.ID, c.Name, c.Country, string(cats), c.Logo, c.Website, c.Network, string(alt), now); err != nil {
			return err
		}
	}
	stStmt, err := tx.Prepare(`INSERT OR IGNORE INTO tv_streams(channel_id, url, quality, user_agent, referrer, alive, cors, fails, checked_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stStmt.Close()
	for _, s := range streams {
		v, ok := old[s.ChannelID+"\n"+s.URL]
		alive, cors, fails, checked := 1, 0, 0, int64(0)
		if ok {
			if !v.alive {
				alive = 0
			}
			cors, fails, checked = b2i(v.cors), v.fails, v.checkedAt
		}
		if _, err := stStmt.Exec(s.ChannelID, s.URL, s.Quality, s.UserAgent, s.Referrer, alive, cors, fails, checked); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO tv_meta(key, value) VALUES ('synced_at', ?)`, now); err != nil {
		return err
	}
	return tx.Commit()
}

// Meta reads a bookkeeping value ("" when unset).
func (r *TVRepo) Meta(key string) string {
	var v string
	_ = r.db.QueryRow(`SELECT value FROM tv_meta WHERE key = ?`, key).Scan(&v)
	return v
}

func (r *TVRepo) SetMeta(key, value string) error {
	_, err := r.db.Exec(`INSERT OR REPLACE INTO tv_meta(key, value) VALUES (?, ?)`, key, value)
	return err
}

// Counts returns channels with at least one alive stream per country and per
// category (a channel counts once per category it carries).
func (r *TVRepo) Counts() (byCountry map[string]int, byCategory map[string]int, err error) {
	rows, err := r.db.Query(`SELECT c.country, c.categories FROM tv_channels c
		WHERE EXISTS (SELECT 1 FROM tv_streams s WHERE s.channel_id = c.id AND s.alive = 1)`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	byCountry, byCategory = map[string]int{}, map[string]int{}
	for rows.Next() {
		var country, cats string
		if err := rows.Scan(&country, &cats); err != nil {
			return nil, nil, err
		}
		byCountry[country]++
		var list []string
		_ = json.Unmarshal([]byte(cats), &list)
		for _, c := range list {
			byCategory[c]++
		}
	}
	return byCountry, byCategory, rows.Err()
}

// TVFilter narrows Channels.
type TVFilter struct {
	Country  string // "" = all configured
	Category string // "" = all
	Query    string // substring of the name, case-insensitive
	UserID   int64  // for the favourite flag / favourites-only
	FavOnly  bool
	Recent   bool // order by the user's last watched, only watched
	Limit    int
}

// Channels lists channels that have an alive stream, with the best quality
// and the user's favourite flag.
func (r *TVRepo) Channels(f TVFilter) ([]TVChannel, error) {
	var sb strings.Builder
	args := []any{f.UserID}
	sb.WriteString(`SELECT c.id, c.name, c.country, c.categories, c.logo, c.website, c.network,
		(SELECT COUNT(*) FROM tv_streams s WHERE s.channel_id = c.id AND s.alive = 1) AS alive,
		COALESCE((SELECT s.quality FROM tv_streams s WHERE s.channel_id = c.id AND s.alive = 1
			ORDER BY CASE s.quality WHEN '2160p' THEN 0 WHEN '1080p' THEN 1 WHEN '720p' THEN 2 WHEN '576p' THEN 3 WHEN '480p' THEN 4 WHEN '' THEN 5 ELSE 6 END LIMIT 1), '') AS quality,
		EXISTS (SELECT 1 FROM tv_favorites f WHERE f.user_id = ? AND f.channel_id = c.id) AS fav`)
	if f.Recent {
		sb.WriteString(`, (SELECT watched_at FROM tv_recent w WHERE w.user_id = ? AND w.channel_id = c.id) AS watched`)
		args = append(args, f.UserID)
	}
	sb.WriteString(` FROM tv_channels c WHERE alive > 0`)
	if f.Country != "" {
		sb.WriteString(` AND c.country = ?`)
		args = append(args, f.Country)
	}
	if f.Category != "" {
		sb.WriteString(` AND c.categories LIKE ?`)
		args = append(args, `%"`+f.Category+`"%`)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		sb.WriteString(` AND lower(c.name) LIKE ?`)
		args = append(args, "%"+strings.ToLower(q)+"%")
	}
	if f.FavOnly {
		sb.WriteString(` AND fav = 1`)
	}
	if f.Recent {
		sb.WriteString(` AND watched IS NOT NULL ORDER BY watched DESC`)
	} else {
		sb.WriteString(` ORDER BY fav DESC, c.name COLLATE NOCASE`)
	}
	if f.Limit > 0 {
		sb.WriteString(` LIMIT ?`)
		args = append(args, f.Limit)
	}
	rows, err := r.db.Query(sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TVChannel{}
	for rows.Next() {
		var c TVChannel
		var cats string
		var fav int
		dest := []any{&c.ID, &c.Name, &c.Country, &cats, &c.Logo, &c.Website, &c.Network, &c.Streams, &c.Quality, &fav}
		if f.Recent {
			var watched sql.NullInt64
			dest = append(dest, &watched)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(cats), &c.Categories)
		if c.Categories == nil {
			c.Categories = []string{}
		}
		c.Favorite = fav == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// Channel returns one channel (alive or not) or sql.ErrNoRows.
func (r *TVRepo) Channel(id string) (TVChannel, error) {
	list, err := r.channelsByIDs([]string{id})
	if err != nil {
		return TVChannel{}, err
	}
	if len(list) == 0 {
		return TVChannel{}, sql.ErrNoRows
	}
	return list[0], nil
}

func (r *TVRepo) channelsByIDs(ids []string) ([]TVChannel, error) {
	out := []TVChannel{}
	for _, id := range ids {
		var c TVChannel
		var cats string
		err := r.db.QueryRow(`SELECT id, name, country, categories, logo, website, network FROM tv_channels WHERE id = ?`, id).
			Scan(&c.ID, &c.Name, &c.Country, &cats, &c.Logo, &c.Website, &c.Network)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(cats), &c.Categories)
		out = append(out, c)
	}
	return out, nil
}

// Streams lists a channel's streams, alive first, best quality first.
func (r *TVRepo) Streams(channelID string) ([]TVStream, error) {
	rows, err := r.db.Query(`SELECT id, channel_id, url, quality, user_agent, referrer, alive, fails, cors FROM tv_streams
		WHERE channel_id = ?
		ORDER BY alive DESC, CASE quality WHEN '2160p' THEN 0 WHEN '1080p' THEN 1 WHEN '720p' THEN 2 WHEN '576p' THEN 3 WHEN '480p' THEN 4 WHEN '' THEN 5 ELSE 6 END, fails`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TVStream
	for rows.Next() {
		var s TVStream
		var alive, cors int
		if err := rows.Scan(&s.ID, &s.ChannelID, &s.URL, &s.Quality, &s.UserAgent, &s.Referrer, &alive, &s.Fails, &cors); err != nil {
			return nil, err
		}
		s.Alive = alive == 1
		s.CORS = cors == 1
		out = append(out, s)
	}
	return out, rows.Err()
}

// AllStreams is for the liveness sweep.
func (r *TVRepo) AllStreams() ([]TVStream, error) {
	rows, err := r.db.Query(`SELECT id, channel_id, url, quality, user_agent, referrer, alive, fails, cors FROM tv_streams`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TVStream
	for rows.Next() {
		var s TVStream
		var alive, cors int
		if err := rows.Scan(&s.ID, &s.ChannelID, &s.URL, &s.Quality, &s.UserAgent, &s.Referrer, &alive, &s.Fails, &cors); err != nil {
			return nil, err
		}
		s.Alive = alive == 1
		s.CORS = cors == 1
		out = append(out, s)
	}
	return out, rows.Err()
}

// MarkStream records one liveness verdict. A stream goes dead after two
// consecutive failures (one bad night must not hide a channel) and comes back
// on the first success.
func (r *TVRepo) MarkStream(id int64, ok, cors bool) error {
	if ok {
		_, err := r.db.Exec(`UPDATE tv_streams SET alive = 1, fails = 0, cors = ?, checked_at = ? WHERE id = ?`, b2i(cors), time.Now().Unix(), id)
		return err
	}
	_, err := r.db.Exec(`UPDATE tv_streams SET fails = fails + 1, alive = CASE WHEN fails + 1 >= 2 THEN 0 ELSE alive END, checked_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

func (r *TVRepo) SetFavorite(userID int64, channelID string, on bool) error {
	if on {
		_, err := r.db.Exec(`INSERT OR IGNORE INTO tv_favorites(user_id, channel_id, added_at) VALUES (?, ?, ?)`, userID, channelID, time.Now().Unix())
		return err
	}
	_, err := r.db.Exec(`DELETE FROM tv_favorites WHERE user_id = ? AND channel_id = ?`, userID, channelID)
	return err
}

// Touch records that the user watched the channel just now (last 50 kept).
func (r *TVRepo) Touch(userID int64, channelID string) error {
	if _, err := r.db.Exec(`INSERT OR REPLACE INTO tv_recent(user_id, channel_id, watched_at) VALUES (?, ?, ?)`, userID, channelID, time.Now().Unix()); err != nil {
		return err
	}
	_, err := r.db.Exec(`DELETE FROM tv_recent WHERE user_id = ? AND channel_id NOT IN (
		SELECT channel_id FROM tv_recent WHERE user_id = ? ORDER BY watched_at DESC LIMIT 50)`, userID, userID)
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- programme guide ------------------------------------------------------------

// ChannelNames lists every channel's spellings for the EPG matcher.
func (r *TVRepo) ChannelNames() ([]TVChannelName, error) {
	rows, err := r.db.Query(`SELECT id, name, country, alt_names FROM tv_channels`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TVChannelName
	for rows.Next() {
		var n TVChannelName
		var alt string
		if err := rows.Scan(&n.ID, &n.Name, &n.Country, &alt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(alt), &n.AltNames)
		out = append(out, n)
	}
	return out, rows.Err()
}

// ReplaceEPG swaps the whole guide and records which feed channel each of
// ours was matched to ("" for the rest).
func (r *TVRepo) ReplaceEPG(progs []TVProgram, epgIDs map[string]string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM tv_programs`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tv_channels SET epg_id = ''`); err != nil {
		return err
	}
	st, err := tx.Prepare(`INSERT OR REPLACE INTO tv_programs(channel_id, start, stop, title, descr) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, p := range progs {
		if _, err := st.Exec(p.ChannelID, p.Start, p.Stop, p.Title, p.Desc); err != nil {
			return err
		}
	}
	for id, x := range epgIDs {
		if _, err := tx.Exec(`UPDATE tv_channels SET epg_id = ? WHERE id = ?`, x, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Programs lists a channel's guide overlapping [from, to).
func (r *TVRepo) Programs(channelID string, from, to int64) ([]TVProgram, error) {
	rows, err := r.db.Query(`SELECT channel_id, start, stop, title, descr FROM tv_programs
		WHERE channel_id = ? AND stop > ? AND start < ? ORDER BY start`, channelID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TVProgram{}
	for rows.Next() {
		var p TVProgram
		if err := rows.Scan(&p.ChannelID, &p.Start, &p.Stop, &p.Title, &p.Desc); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// NowNext returns, for every channel with a guide, what is on at `now` and
// what follows (titles only — the overlay's channel list).
func (r *TVRepo) NowNext(now int64) (map[string]*TVNowNext, error) {
	out := map[string]*TVNowNext{}
	get := func(id string) *TVNowNext {
		if v, ok := out[id]; ok {
			return v
		}
		v := &TVNowNext{}
		out[id] = v
		return v
	}
	rows, err := r.db.Query(`SELECT channel_id, start, stop, title FROM tv_programs WHERE start <= ? AND stop > ?`, now, now)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p TVProgram
		if err := rows.Scan(&p.ChannelID, &p.Start, &p.Stop, &p.Title); err != nil {
			rows.Close()
			return nil, err
		}
		get(p.ChannelID).Now = &p
	}
	rows.Close()
	rows, err = r.db.Query(`SELECT p.channel_id, p.start, p.stop, p.title FROM tv_programs p
		WHERE p.start > ? AND p.start = (SELECT MIN(q.start) FROM tv_programs q WHERE q.channel_id = p.channel_id AND q.start > ?)`, now, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p TVProgram
		if err := rows.Scan(&p.ChannelID, &p.Start, &p.Stop, &p.Title); err != nil {
			return nil, err
		}
		get(p.ChannelID).Next = &p
	}
	return out, rows.Err()
}
