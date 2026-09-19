package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
	if _, err := tx.Exec(`UPDATE tv_channels SET title = COALESCE((SELECT t.title FROM tv_channel_titles t WHERE t.channel_id = tv_channels.id), '')`); err != nil {
		return err
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
func (r *TVRepo) Counts(countries []string) (byCountry map[string]int, byCategory map[string]int, err error) {
	q := `SELECT c.country, c.categories FROM tv_channels c
		WHERE EXISTS (SELECT 1 FROM tv_streams s WHERE s.channel_id = c.id AND s.alive = 1)`
	var args []any
	if len(countries) > 0 {
		q += ` AND c.country IN (` + placeholders(len(countries)) + `)`
		for _, c := range countries {
			args = append(args, c)
		}
	}
	rows, err := r.db.Query(q, args...)
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
	ID        string   // "" = any; one channel by id (Mini App "open on TV")
	Country   string   // "" = all configured
	Countries []string // the profile's allowed countries; nil = no restriction
	Category  string   // "" = all
	Query     string   // substring of the name, case-insensitive
	UserID    int64    // for the favourite flag / favourites-only
	FavOnly   bool
	Recent    bool // order by the user's last watched, only watched
	Limit     int
}

// Channels lists channels that have an alive stream, with the best quality
// and the user's favourite flag.
func (r *TVRepo) Channels(f TVFilter) ([]TVChannel, error) {
	var sb strings.Builder
	args := []any{f.UserID}
	sb.WriteString(`SELECT c.id, CASE WHEN c.title != '' THEN c.title ELSE c.name END AS name, c.country, c.categories, c.logo, c.website, c.network,
		(SELECT COUNT(*) FROM tv_streams s WHERE s.channel_id = c.id AND s.alive = 1) AS alive,
		COALESCE((SELECT s.quality FROM tv_streams s WHERE s.channel_id = c.id AND s.alive = 1
			ORDER BY CASE s.quality WHEN '2160p' THEN 0 WHEN '1080p' THEN 1 WHEN '720p' THEN 2 WHEN '576p' THEN 3 WHEN '480p' THEN 4 WHEN '' THEN 5 ELSE 6 END LIMIT 1), '') AS quality,
		EXISTS (SELECT 1 FROM tv_favorites f WHERE f.user_id = ? AND f.channel_id = c.id) AS fav`)
	if f.Recent {
		sb.WriteString(`, (SELECT watched_at FROM tv_recent w WHERE w.user_id = ? AND w.channel_id = c.id) AS watched`)
		args = append(args, f.UserID)
	}
	sb.WriteString(` FROM tv_channels c WHERE alive > 0`)
	if f.ID != "" {
		sb.WriteString(` AND c.id = ?`)
		args = append(args, f.ID)
	}
	if f.Country != "" {
		sb.WriteString(` AND c.country = ?`)
		args = append(args, f.Country)
	}
	if len(f.Countries) > 0 {
		sb.WriteString(` AND c.country IN (` + placeholders(len(f.Countries)) + `)`)
		for _, c := range f.Countries {
			args = append(args, c)
		}
	}
	if f.Category != "" {
		sb.WriteString(` AND c.categories LIKE ?`)
		args = append(args, `%"`+f.Category+`"%`)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		sb.WriteString(` AND (lower(c.name) LIKE ? OR lower(c.title) LIKE ?)`)
		args = append(args, "%"+strings.ToLower(q)+"%", "%"+strings.ToLower(q)+"%")
	}
	if f.FavOnly {
		sb.WriteString(` AND fav = 1`)
	}
	if f.Recent {
		sb.WriteString(` AND watched IS NOT NULL ORDER BY watched DESC`)
	} else {
		sb.WriteString(` ORDER BY fav DESC, name COLLATE NOCASE`)
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
		err := r.db.QueryRow(`SELECT id, CASE WHEN title != '' THEN title ELSE name END, country, categories, logo, website, network FROM tv_channels WHERE id = ?`, id).
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
	rows, err := r.db.Query(`SELECT id, name, country, alt_names, title FROM tv_channels`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TVChannelName
	for rows.Next() {
		var n TVChannelName
		var alt, title string
		if err := rows.Scan(&n.ID, &n.Name, &n.Country, &alt, &title); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(alt), &n.AltNames)
		if title != "" {
			n.AltNames = append(n.AltNames, title)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// EPGWriter streams one feed's programmes into tv_epg_programs inside a
// transaction (a feed is hundreds of thousands of rows — never all in memory).
type EPGWriter struct {
	db *sql.DB
	tx *sql.Tx
	st *sql.Stmt
	n  int
}

// BeginEPG drops the source's old rows and returns a writer for the new ones.
func (r *TVRepo) BeginEPG(source string) (*EPGWriter, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM tv_epg_programs WHERE key LIKE ?`, source+":%"); err != nil {
		tx.Rollback()
		return nil, err
	}
	st, err := tx.Prepare(`INSERT OR REPLACE INTO tv_epg_programs(key, start, stop, title, descr) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return &EPGWriter{db: r.db, tx: tx, st: st}, nil
}

// epgChunk rows per transaction: a feed is ~20 s of inserts, and one long
// write transaction would hold the SQLite lock past busy_timeout for every
// other writer (an admin click got SQLITE_BUSY). Readers may briefly see a
// half-replaced feed twice a day; that beats blocked writes.
const epgChunk = 5000

func (w *EPGWriter) Add(key string, p TVProgram) error {
	if w.n > 0 && w.n%epgChunk == 0 {
		w.st.Close()
		if err := w.tx.Commit(); err != nil {
			return err
		}
		tx, err := w.db.Begin()
		if err != nil {
			return err
		}
		st, err := tx.Prepare(`INSERT OR REPLACE INTO tv_epg_programs(key, start, stop, title, descr) VALUES (?, ?, ?, ?, ?)`)
		if err != nil {
			tx.Rollback()
			return err
		}
		w.tx, w.st = tx, st
	}
	_, err := w.st.Exec(key, p.Start, p.Stop, p.Title, p.Desc)
	if err == nil {
		w.n++
	}
	return err
}

func (w *EPGWriter) Count() int { return w.n }

func (w *EPGWriter) Commit() error {
	w.st.Close()
	return w.tx.Commit()
}

func (w *EPGWriter) Rollback() {
	w.st.Close()
	_ = w.tx.Rollback()
}

// EPGID of one channel ("" = no guide / unknown channel).
func (r *TVRepo) EPGID(channelID string) string {
	var key string
	_ = r.db.QueryRow(`SELECT epg_id FROM tv_channels WHERE id = ?`, channelID).Scan(&key)
	return key
}

// SetEPGIDs points our channels at feed channels ("" = no guide). Channels
// not in the map are left alone.
func (r *TVRepo) SetEPGIDs(ids map[string]string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, key := range ids {
		if _, err := tx.Exec(`UPDATE tv_channels SET epg_id = ? WHERE id = ?`, key, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Programs lists a channel's guide overlapping [from, to).
func (r *TVRepo) Programs(channelID string, from, to int64) ([]TVProgram, error) {
	rows, err := r.db.Query(`SELECT p.start, p.stop, p.title, p.descr FROM tv_epg_programs p
		WHERE p.key = (SELECT epg_id FROM tv_channels WHERE id = ?) AND p.stop > ? AND p.start < ? ORDER BY p.start`, channelID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TVProgram{}
	for rows.Next() {
		p := TVProgram{ChannelID: channelID}
		if err := rows.Scan(&p.Start, &p.Stop, &p.Title, &p.Desc); err != nil {
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
	rows, err := r.db.Query(`SELECT c.id, p.start, p.stop, p.title FROM tv_channels c
		JOIN tv_epg_programs p ON p.key = c.epg_id WHERE c.epg_id != '' AND p.start <= ? AND p.stop > ?`, now, now)
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
	rows, err = r.db.Query(`SELECT c.id, p.start, p.stop, p.title FROM tv_channels c
		JOIN tv_epg_programs p ON p.key = c.epg_id
		WHERE c.epg_id != '' AND p.start > ? AND p.start = (SELECT MIN(q.start) FROM tv_epg_programs q WHERE q.key = p.key AND q.start > ?)`, now, now)
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

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// ---- admin: EPG overrides, feed channels, stats ---------------------------------

// EPGOverride pins a channel to a feed channel; XMLTVID "" = no guide.
type EPGOverride struct {
	ChannelID string `json:"channel_id"`
	Source    string `json:"source"`
	XMLTVID   string `json:"xmltv_id"`
}

func (r *TVRepo) EPGOverrides() (map[string]EPGOverride, error) {
	rows, err := r.db.Query(`SELECT channel_id, source, xmltv_id FROM tv_epg_overrides`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]EPGOverride{}
	for rows.Next() {
		var o EPGOverride
		if err := rows.Scan(&o.ChannelID, &o.Source, &o.XMLTVID); err != nil {
			return nil, err
		}
		out[o.ChannelID] = o
	}
	return out, rows.Err()
}

func (r *TVRepo) SetEPGOverride(o EPGOverride) error {
	_, err := r.db.Exec(`INSERT OR REPLACE INTO tv_epg_overrides(channel_id, source, xmltv_id) VALUES (?, ?, ?)`, o.ChannelID, o.Source, o.XMLTVID)
	return err
}

func (r *TVRepo) DeleteEPGOverride(channelID string) error {
	_, err := r.db.Exec(`DELETE FROM tv_epg_overrides WHERE channel_id = ?`, channelID)
	return err
}

// EPGChannel is one channel of a feed (for the admin's picker).
type EPGChannel struct {
	Source  string   `json:"source"`
	XMLTVID string   `json:"xmltv_id"`
	Names   []string `json:"names"`
}

func (r *TVRepo) ReplaceEPGChannels(source string, list []EPGChannel) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM tv_epg_channels WHERE source = ?`, source); err != nil {
		return err
	}
	st, err := tx.Prepare(`INSERT OR REPLACE INTO tv_epg_channels(source, xmltv_id, names, names_lc) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, c := range list {
		names := strings.Join(c.Names, " | ")
		if _, err := st.Exec(source, c.XMLTVID, names, strings.ToLower(names)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// EPGChannels lists one feed's channels (for re-matching without a download).
func (r *TVRepo) EPGChannels(source string) ([]EPGChannel, error) {
	rows, err := r.db.Query(`SELECT source, xmltv_id, names FROM tv_epg_channels WHERE source = ?`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EPGChannel{}
	for rows.Next() {
		var c EPGChannel
		var names string
		if err := rows.Scan(&c.Source, &c.XMLTVID, &names); err != nil {
			return nil, err
		}
		c.Names = strings.Split(names, " | ")
		out = append(out, c)
	}
	return out, rows.Err()
}

// SearchEPGChannels: substring of any display name or the id, case-insensitive.
func (r *TVRepo) SearchEPGChannels(q string, limit int) ([]EPGChannel, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return []EPGChannel{}, nil
	}
	rows, err := r.db.Query(`SELECT source, xmltv_id, names FROM tv_epg_channels
		WHERE names_lc LIKE ? OR lower(xmltv_id) LIKE ? ORDER BY length(names), source LIMIT ?`, "%"+q+"%", "%"+q+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EPGChannel{}
	for rows.Next() {
		var c EPGChannel
		var names string
		if err := rows.Scan(&c.Source, &c.XMLTVID, &names); err != nil {
			return nil, err
		}
		c.Names = strings.Split(names, " | ")
		out = append(out, c)
	}
	return out, rows.Err()
}

// AdminChannel is a catalogue row with its guide mapping, for the admin panel.
type AdminChannel struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Title    string   `json:"title"` // our own display name, "" = catalogue name
	Country  string   `json:"country"`
	Alive    int      `json:"alive"`
	EPGID    string   `json:"epg_id"`   // "<source>:<xmltv id>", "" = no guide
	EPGName  string   `json:"epg_name"` // the feed channel's first display name
	Override string   `json:"override"` // "<source>:<xmltv id>", "none" (pinned to no guide) or ""
	AltNames []string `json:"alt_names"`
}

func (r *TVRepo) AdminChannels(q, country string, noEPG bool, limit int) ([]AdminChannel, error) {
	var sb strings.Builder
	var args []any
	sb.WriteString(`SELECT c.id, c.name, c.title, c.country, c.epg_id, c.alt_names,
		(SELECT COUNT(*) FROM tv_streams s WHERE s.channel_id = c.id AND s.alive = 1) AS alive,
		COALESCE(o.source, ''), COALESCE(o.xmltv_id, ''), o.channel_id IS NOT NULL,
		COALESCE((SELECT e.names FROM tv_epg_channels e WHERE e.source || ':' || e.xmltv_id = c.epg_id), '')
		FROM tv_channels c LEFT JOIN tv_epg_overrides o ON o.channel_id = c.id WHERE 1 = 1`)
	if q = strings.ToLower(strings.TrimSpace(q)); q != "" {
		sb.WriteString(` AND (lower(c.name) LIKE ? OR lower(c.id) LIKE ? OR lower(c.alt_names) LIKE ? OR lower(c.title) LIKE ?)`)
		args = append(args, "%"+q+"%", "%"+q+"%", "%"+q+"%", "%"+q+"%")
	}
	if country != "" {
		sb.WriteString(` AND c.country = ?`)
		args = append(args, country)
	}
	if noEPG {
		sb.WriteString(` AND c.epg_id = ''`)
	}
	sb.WriteString(` ORDER BY alive DESC, c.name COLLATE NOCASE LIMIT ?`)
	args = append(args, limit)
	rows, err := r.db.Query(sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminChannel{}
	for rows.Next() {
		var c AdminChannel
		var src, xid, alt, names string
		var has bool
		if err := rows.Scan(&c.ID, &c.Name, &c.Title, &c.Country, &c.EPGID, &alt, &c.Alive, &src, &xid, &has, &names); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(alt), &c.AltNames)
		if c.AltNames == nil {
			c.AltNames = []string{}
		}
		if names != "" {
			c.EPGName = strings.SplitN(names, " | ", 2)[0]
		}
		if has {
			if xid == "" {
				c.Override = "none"
			} else {
				c.Override = src + ":" + xid
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TVStats for the admin status card.
type TVStats struct {
	Channels   int   `json:"channels"`
	Alive      int   `json:"alive"` // channels with an alive stream
	Streams    int   `json:"streams"`
	Programmes int   `json:"programmes"`
	WithEPG    int   `json:"with_epg"`
	SyncedAt   int64 `json:"synced_at"`
	CheckedAt  int64 `json:"checked_at"`
	EPGAt      int64 `json:"epg_at"`
}

func (r *TVRepo) Stats() (TVStats, error) {
	var st TVStats
	q := func(sql string, dst *int) error { return r.db.QueryRow(sql).Scan(dst) }
	if err := q(`SELECT COUNT(*) FROM tv_channels`, &st.Channels); err != nil {
		return st, err
	}
	if err := q(`SELECT COUNT(*) FROM tv_channels c WHERE EXISTS (SELECT 1 FROM tv_streams s WHERE s.channel_id = c.id AND s.alive = 1)`, &st.Alive); err != nil {
		return st, err
	}
	if err := q(`SELECT COUNT(*) FROM tv_streams`, &st.Streams); err != nil {
		return st, err
	}
	if err := q(`SELECT COUNT(*) FROM tv_epg_programs`, &st.Programmes); err != nil {
		return st, err
	}
	if err := q(`SELECT COUNT(*) FROM tv_channels WHERE epg_id != ''`, &st.WithEPG); err != nil {
		return st, err
	}
	var v int64
	for k, dst := range map[string]*int64{"synced_at": &st.SyncedAt, "checked_at": &st.CheckedAt, "epg_at": &st.EPGAt} {
		v = 0
		_, _ = fmt.Sscan(r.Meta(k), &v)
		*dst = v
	}
	return st, nil
}

// SetTitle stores our display name for a channel ("" = back to the catalogue name).
func (r *TVRepo) SetTitle(channelID, title string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if title == "" {
		if _, err := tx.Exec(`DELETE FROM tv_channel_titles WHERE channel_id = ?`, channelID); err != nil {
			return err
		}
	} else if _, err := tx.Exec(`INSERT OR REPLACE INTO tv_channel_titles(channel_id, title) VALUES (?, ?)`, channelID, title); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tv_channels SET title = ? WHERE id = ?`, title, channelID); err != nil {
		return err
	}
	return tx.Commit()
}
