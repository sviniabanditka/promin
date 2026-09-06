package telegram

import (
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sviniabanditka/promin/server/internal/catalog"
	promsync "github.com/sviniabanditka/promin/server/internal/sync"
)

// pageSize is how many results one Telegram message shows.
const pageSize = 5

// Callback data (≤ 64 bytes):
//
//	open:<tmdb_id>:<media_type>:<page>
//	page:<q-hash>:<n>
//	dev:<device_id>:<tmdb_id>:<media_type>
type callback struct {
	Kind      string // open | page | dev
	TMDBID    int
	MediaType string
	Page      int
	QueryHash string
	DeviceID  string
}

var errBadCallback = errors.New("telegram: bad callback data")

func (c callback) String() string {
	switch c.Kind {
	case "open":
		return fmt.Sprintf("open:%d:%s:%d", c.TMDBID, c.MediaType, c.Page)
	case "page":
		return fmt.Sprintf("page:%s:%d", c.QueryHash, c.Page)
	case "dev":
		return fmt.Sprintf("dev:%s:%d:%s", c.DeviceID, c.TMDBID, c.MediaType)
	}
	return ""
}

func parseCallback(data string) (callback, error) {
	p := strings.Split(data, ":")
	var c callback
	var err error
	switch {
	case len(p) == 4 && p[0] == "open":
		c.Kind, c.MediaType = "open", p[2]
		if c.TMDBID, err = strconv.Atoi(p[1]); err == nil {
			c.Page, err = strconv.Atoi(p[3])
		}
	case len(p) == 3 && p[0] == "page":
		c.Kind, c.QueryHash = "page", p[1]
		c.Page, err = strconv.Atoi(p[2])
	case len(p) == 4 && p[0] == "dev":
		c.Kind, c.DeviceID, c.MediaType = "dev", p[1], p[3]
		c.TMDBID, err = strconv.Atoi(p[2])
	default:
		return callback{}, errBadCallback
	}
	if err != nil || (c.Kind != "page" && c.MediaType != "movie" && c.MediaType != "tv") {
		return callback{}, errBadCallback
	}
	return c, nil
}

// queryHash is a short stable tag for a query so a stale ◀/▶ press on an
// old message can be detected.
func queryHash(q string) string {
	h := fnv.New32a()
	h.Write([]byte(q))
	return fmt.Sprintf("%08x", h.Sum32())
}

// searchPage renders one page of results: the text and the inline keyboard.
// hasMore tells whether anything follows page (a further TMDB page may
// still be unfetched).
func searchPage(q string, items []catalog.Title, page int, hasMore bool) (string, *InlineKeyboardMarkup) {
	if page < 1 {
		page = 1
	}
	start := (page - 1) * pageSize
	if start > len(items) {
		start = len(items)
	}
	end := min(start+pageSize, len(items))
	slice := items[start:end]

	var b strings.Builder
	fmt.Fprintf(&b, "🔎 «%s» — сторінка %d\n\n", q, page)
	if len(slice) == 0 {
		b.WriteString("Нічого не знайдено.")
	}
	rows := make([][]InlineKeyboardButton, 0, len(slice)+1)
	for i, t := range slice {
		icon := "🎬"
		if t.Type == "tv" {
			icon = "📺"
		}
		year := ""
		if t.Year > 0 {
			year = fmt.Sprintf(" (%d)", t.Year)
		}
		fmt.Fprintf(&b, "%d. %s %s%s\n", start+i+1, icon, t.Title, year)
		rows = append(rows, []InlineKeyboardButton{{
			Text:         "Відкрити на ТВ: " + shorten(t.Title, 28),
			CallbackData: callback{Kind: "open", TMDBID: t.TMDBID, MediaType: t.Type, Page: page}.String(),
		}})
	}
	var nav []InlineKeyboardButton
	if page > 1 {
		nav = append(nav, InlineKeyboardButton{Text: "◀", CallbackData: callback{Kind: "page", QueryHash: queryHash(q), Page: page - 1}.String()})
	}
	if end < len(items) || hasMore {
		nav = append(nav, InlineKeyboardButton{Text: "▶", CallbackData: callback{Kind: "page", QueryHash: queryHash(q), Page: page + 1}.String()})
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	return strings.TrimRight(b.String(), "\n"), &InlineKeyboardMarkup{InlineKeyboard: rows}
}

// openReply decides what "Відкрити на ТВ" does given the user's online
// devices: target is the device to publish to (nil when the user must pick
// or nothing is online); text/markup is the reply to send.
func openReply(devs []promsync.DeviceInfo, tmdbID int, mediaType string) (target *promsync.DeviceInfo, text string, markup *InlineKeyboardMarkup) {
	switch len(devs) {
	case 0:
		return nil, "Немає пристроїв онлайн — відкрий Promin на телевізорі", nil
	case 1:
		return &devs[0], "Відкрито на " + deviceLabel(devs[0]), nil
	}
	rows := make([][]InlineKeyboardButton, 0, len(devs))
	for _, d := range devs {
		rows = append(rows, []InlineKeyboardButton{{
			Text:         deviceLabel(d),
			CallbackData: callback{Kind: "dev", DeviceID: d.ID, TMDBID: tmdbID, MediaType: mediaType}.String(),
		}})
	}
	return nil, "На якому пристрої відкрити?", &InlineKeyboardMarkup{InlineKeyboard: rows}
}

func deviceLabel(d promsync.DeviceInfo) string {
	if d.Name != "" {
		return d.Name
	}
	return "пристрій " + d.ID
}

func shorten(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
