package telegram

import (
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sviniabanditka/promin/server/internal/catalog"
	"github.com/sviniabanditka/promin/server/internal/store"
	promsync "github.com/sviniabanditka/promin/server/internal/sync"
)

// pageSize is how many results one Telegram message shows.
const pageSize = 5

// Callback data (≤ 64 bytes):
//
//	open:<tmdb_id>:<media_type>:<r>   open on TV; r=1 resumes from the saved position
//	bm:<tmdb_id>:<media_type>:<page>  toggle bookmark, re-render <page> of this message's listing
//	page:<n>                          page n of this message's listing
//	dev:<device_id>                   device picked for the chat's pending action
//	home:<row_id>                     show a home row ("what to watch")
//	rc:<action>                       remote: play|back|fwd|prev|next|mute|night|sleep
//	lang:<code>                       set interface language
//	unlink:ask|yes|no
type callback struct {
	Kind      string
	TMDBID    int
	MediaType string
	Page      int
	Resume    bool
	Arg       string // device id | row id | remote action | lang | unlink step
}

var errBadCallback = errors.New("telegram: bad callback data")

func (c callback) String() string {
	switch c.Kind {
	case "open":
		r := 0
		if c.Resume {
			r = 1
		}
		return fmt.Sprintf("open:%d:%s:%d", c.TMDBID, c.MediaType, r)
	case "bm":
		return fmt.Sprintf("bm:%d:%s:%d", c.TMDBID, c.MediaType, c.Page)
	case "page":
		return fmt.Sprintf("page:%d", c.Page)
	case "dev", "home", "rc", "lang", "unlink":
		return c.Kind + ":" + c.Arg
	}
	return ""
}

func parseCallback(data string) (callback, error) {
	p := strings.Split(data, ":")
	var c callback
	var err error
	switch {
	case len(p) == 4 && (p[0] == "open" || p[0] == "bm"):
		c.Kind, c.MediaType = p[0], p[2]
		if c.TMDBID, err = strconv.Atoi(p[1]); err == nil {
			c.Page, err = strconv.Atoi(p[3])
		}
		c.Resume = c.Kind == "open" && c.Page == 1
		if c.MediaType != "movie" && c.MediaType != "tv" {
			return callback{}, errBadCallback
		}
	case len(p) == 2 && p[0] == "page":
		c.Kind = "page"
		c.Page, err = strconv.Atoi(p[1])
	case len(p) == 2 && p[1] != "" && (p[0] == "dev" || p[0] == "home" || p[0] == "rc" || p[0] == "lang" || p[0] == "unlink"):
		c.Kind, c.Arg = p[0], p[1]
	default:
		return callback{}, errBadCallback
	}
	if err != nil {
		return callback{}, errBadCallback
	}
	return c, nil
}

// listing is what one bot message lists; kept per message so ◀ ▶ and ★
// can re-render it without re-sending the query in callback data.
type listing struct {
	kind       string // search | bookmarks | home | continue
	q          string // search query
	rowTitle   string // home row title
	items      []catalog.Title
	tcs        []store.Timecode // continue: parallel to items
	tmdbPages  int              // search: TMDB pages fetched so far
	totalPages int              // search: TMDB total pages
}

// hasMore reports whether a further, not yet fetched TMDB page exists.
func (l *listing) hasMore() bool { return l.kind == "search" && l.tmdbPages < l.totalPages }

// slice returns the items of page (1-based) and their start offset.
func (l *listing) slice(page int) (start int, items []catalog.Title) {
	if l.kind == "continue" {
		return 0, l.items
	}
	start = (max(page, 1) - 1) * pageSize
	if start > len(l.items) {
		start = len(l.items)
	}
	return start, l.items[start:min(start+pageSize, len(l.items))]
}

func bmKey(mediaType string, tmdbID int) string { return mediaType + ":" + strconv.Itoa(tmdbID) }

func icon(mediaType string) string {
	if mediaType == "tv" {
		return "📺"
	}
	return "🎬"
}

// renderList renders one page of l: HTML text plus the inline keyboard.
// bm is the user's bookmark set (bmKey) for the ★/☆ toggles.
func renderList(lang string, l *listing, page int, bm map[string]bool) (string, *InlineKeyboardMarkup) {
	page = max(page, 1)
	start, slice := l.slice(page)

	var sb strings.Builder
	switch l.kind {
	case "search":
		sb.WriteString(tr(lang, "search.header", html.EscapeString(l.q), page))
	case "bookmarks":
		sb.WriteString(tr(lang, "bm.header", page))
	case "home":
		sb.WriteString(tr(lang, "watch.page", html.EscapeString(l.rowTitle), page))
	case "continue":
		sb.WriteString(tr(lang, "continue.header"))
	}
	sb.WriteString("\n\n")
	if len(slice) == 0 {
		sb.WriteString(tr(lang, map[string]string{"search": "search.empty", "bookmarks": "bm.empty", "home": "watch.empty", "continue": "continue.empty"}[l.kind]))
	}

	rows := make([][]InlineKeyboardButton, 0, len(slice)+1)
	for i, t := range slice {
		name := t.Title
		if name == "" {
			name = "#" + strconv.Itoa(t.TMDBID)
		}
		open := InlineKeyboardButton{
			Text:         tr(lang, "open.btn") + ": " + shorten(name, 24),
			CallbackData: callback{Kind: "open", TMDBID: t.TMDBID, MediaType: t.Type, Resume: l.kind == "continue"}.String(),
		}
		if l.kind == "continue" {
			sb.WriteString(continueLine(t, l.tcs[i]) + "\n")
			rows = append(rows, []InlineKeyboardButton{open})
			continue
		}
		year := ""
		if t.Year > 0 {
			year = fmt.Sprintf(" (%d)", t.Year)
		}
		fmt.Fprintf(&sb, "%d. %s <b>%s</b>%s\n", start+i+1, icon(t.Type), html.EscapeString(name), year)
		star := "☆"
		switch {
		case l.kind == "bookmarks":
			star = tr(lang, "bm.remove")
		case bm[bmKey(t.Type, t.TMDBID)]:
			star = "★"
		}
		rows = append(rows, []InlineKeyboardButton{open, {
			Text:         star,
			CallbackData: callback{Kind: "bm", TMDBID: t.TMDBID, MediaType: t.Type, Page: page}.String(),
		}})
	}
	var nav []InlineKeyboardButton
	if page > 1 && l.kind != "continue" {
		nav = append(nav, InlineKeyboardButton{Text: "◀", CallbackData: callback{Kind: "page", Page: page - 1}.String()})
	}
	if start+len(slice) < len(l.items) || l.hasMore() {
		nav = append(nav, InlineKeyboardButton{Text: "▶", CallbackData: callback{Kind: "page", Page: page + 1}.String()})
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	return strings.TrimRight(sb.String(), "\n"), &InlineKeyboardMarkup{InlineKeyboard: rows}
}

// continueLine formats "▶ <b>Title</b> (S2E5 · 42:10 / 1:02:00)".
func continueLine(t catalog.Title, tc store.Timecode) string {
	name := t.Title
	if name == "" {
		name = "#" + strconv.Itoa(t.TMDBID)
	}
	var meta []string
	if t.Type == "tv" && tc.Episode > 0 {
		meta = append(meta, fmt.Sprintf("S%dE%d", tc.Season, tc.Episode))
	}
	if tc.DurationSec > 0 {
		meta = append(meta, clock(tc.PositionSec)+" / "+clock(tc.DurationSec))
	} else if tc.PositionSec > 0 {
		meta = append(meta, clock(tc.PositionSec))
	}
	s := "▶ " + icon(t.Type) + " <b>" + html.EscapeString(name) + "</b>"
	if len(meta) > 0 {
		s += " (" + strings.Join(meta, " · ") + ")"
	}
	return s
}

// clock formats seconds as m:ss or h:mm:ss.
func clock(sec float64) string {
	s := int(sec)
	h, m, ss := s/3600, s%3600/60, s%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, ss)
	}
	return fmt.Sprintf("%d:%02d", m, ss)
}

// devicePicker lists online devices, one button per row (dev:<id>).
func devicePicker(devs []promsync.DeviceInfo, lang string) *InlineKeyboardMarkup {
	rows := make([][]InlineKeyboardButton, 0, len(devs))
	for _, d := range devs {
		rows = append(rows, []InlineKeyboardButton{{Text: deviceLabel(d, lang), CallbackData: callback{Kind: "dev", Arg: d.ID}.String()}})
	}
	return &InlineKeyboardMarkup{InlineKeyboard: rows}
}

func deviceLabel(d promsync.DeviceInfo, lang string) string {
	if d.Name != "" {
		return d.Name
	}
	return tr(lang, "device", d.ID)
}

// homeMenu offers the home rows as buttons (home:<row_id>).
func homeMenu(rows []catalog.Row) *InlineKeyboardMarkup {
	kb := &InlineKeyboardMarkup{}
	for _, r := range rows {
		if len(r.Items) == 0 {
			continue
		}
		kb.InlineKeyboard = append(kb.InlineKeyboard, []InlineKeyboardButton{{Text: r.Title, CallbackData: callback{Kind: "home", Arg: r.ID}.String()}})
	}
	return kb
}

// RemotePayload is the sync.EventRemote payload.
type RemotePayload struct {
	DeviceID string  `json:"device_id"`
	Action   string  `json:"action"` // toggle_play | seek | seek_to | prev | next | mute | night | sleep | set_local
	Value    float64 `json:"value"`  // seek: seconds (±30); seek_to: absolute seconds; sleep: minutes
	// set_local: a device-local TV setting (legacy_tv_mode | reduce_motion |
	// debug_mode) and its new value ("true" | "false").
	Key string `json:"key,omitempty"`
	Str string `json:"str,omitempty"`
}

// remoteActions maps rc:<key> to the published action/value.
var remoteActions = map[string]RemotePayload{
	"play":  {Action: "toggle_play"},
	"back":  {Action: "seek", Value: -30},
	"fwd":   {Action: "seek", Value: 30},
	"prev":  {Action: "prev"},
	"next":  {Action: "next"},
	"mute":  {Action: "mute"},
	"night": {Action: "night"},
	"sleep": {Action: "sleep", Value: 30},
}

// remotePayload builds the EventRemote payload for rc:<key>; ok=false for
// an unknown key.
func remotePayload(deviceID, key string) (RemotePayload, bool) {
	p, ok := remoteActions[key]
	p.DeviceID = deviceID
	return p, ok
}

func remoteKeyboard(lang string) *InlineKeyboardMarkup {
	rc := func(text, key string) InlineKeyboardButton {
		return InlineKeyboardButton{Text: text, CallbackData: callback{Kind: "rc", Arg: key}.String()}
	}
	return &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
		{rc("⏪ 30", "back"), rc("⏯", "play"), rc("⏩ 30", "fwd")},
		{rc("⏮", "prev"), rc("⏭", "next")},
		{rc("🔇", "mute"), rc(tr(lang, "remote.night"), "night"), rc(tr(lang, "remote.sleep"), "sleep")},
	}}
}

// settingsKeyboard: language row (current one ticked) + Unlink.
func settingsKeyboard(lang string) *InlineKeyboardMarkup {
	var row []InlineKeyboardButton
	for _, l := range langs {
		text := langNames[l]
		if l == lang {
			text = "✓ " + text
		}
		row = append(row, InlineKeyboardButton{Text: text, CallbackData: callback{Kind: "lang", Arg: l}.String()})
	}
	return &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
		row,
		{{Text: tr(lang, "settings.unlink"), CallbackData: callback{Kind: "unlink", Arg: "ask"}.String()}},
	}}
}

func unlinkConfirm(lang string) *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
		{Text: tr(lang, "unlink.yes"), CallbackData: callback{Kind: "unlink", Arg: "yes"}.String()},
		{Text: tr(lang, "unlink.no"), CallbackData: callback{Kind: "unlink", Arg: "no"}.String()},
	}}}
}

func shorten(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
