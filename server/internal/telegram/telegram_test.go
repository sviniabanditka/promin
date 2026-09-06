package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sviniabanditka/promin/server/internal/catalog"
	"github.com/sviniabanditka/promin/server/internal/store"
	promsync "github.com/sviniabanditka/promin/server/internal/sync"
)

func TestI18nParity(t *testing.T) {
	base := texts[defaultLang]
	for _, l := range langs {
		if len(texts[l]) != len(base) {
			t.Fatalf("%s has %d keys, %s has %d", l, len(texts[l]), defaultLang, len(base))
		}
		for k, v := range base {
			if texts[l][k] == "" {
				t.Fatalf("%s: missing key %q", l, k)
			}
			if strings.Count(v, "%") != strings.Count(texts[l][k], "%") {
				t.Fatalf("%s: %q has a different number of format verbs", l, k)
			}
		}
	}
	if tr("xx", "hello") != texts["uk"]["hello"] || tr("en", "open.done", "TV") != "Opened on TV" {
		t.Fatal("tr fallback/format")
	}
	if normLang("ru-RU") != "ru" || normLang("") != "uk" || normLang("de") != "uk" {
		t.Fatal("normLang")
	}
}

func TestMainMenuMatching(t *testing.T) {
	for _, l := range langs {
		kb := mainMenu(l)
		if len(kb.Keyboard) != 3 || !kb.ResizeKeyboard {
			t.Fatalf("%s: keyboard %+v", l, kb)
		}
		var i int
		for _, row := range kb.Keyboard {
			for _, btn := range row {
				if got := menuAction(btn.Text); got != menuActions[i] {
					t.Fatalf("%s: %q -> %q, want %q", l, btn.Text, got, menuActions[i])
				}
				i++
			}
		}
	}
	if menuAction("Interstellar") != "" || menuAction("") != "" {
		t.Fatal("plain text must not match the menu")
	}
	if len(botCommands("en")) != 4 {
		t.Fatal("bot commands")
	}
}

func TestCallbackRoundTrip(t *testing.T) {
	for _, c := range []callback{
		{Kind: "open", TMDBID: 550, MediaType: "movie"},
		{Kind: "open", TMDBID: 1399, MediaType: "tv", Resume: true, Page: 1},
		{Kind: "bm", TMDBID: 550, MediaType: "movie", Page: 3},
		{Kind: "page", Page: 3},
		{Kind: "dev", Arg: "abcDEF123456"},
		{Kind: "home", Arg: "trending"},
		{Kind: "rc", Arg: "sleep"},
		{Kind: "lang", Arg: "en"},
		{Kind: "unlink", Arg: "ask"},
	} {
		s := c.String()
		if len(s) > 64 {
			t.Fatalf("%q exceeds 64 bytes", s)
		}
		got, err := parseCallback(s)
		if err != nil || got != c {
			t.Fatalf("%q -> %+v, %v (want %+v)", s, got, err, c)
		}
	}
	if s := (callback{Kind: "open", TMDBID: 1, MediaType: "tv", Resume: true}).String(); s != "open:1:tv:1" {
		t.Fatalf("open resume: %q", s)
	}
	for _, bad := range []string{"", "open:x:movie:1", "open:1:book:1", "bm:1:movie", "page:h", "nope:1", "dev:", "rc"} {
		if _, err := parseCallback(bad); err == nil {
			t.Fatalf("%q must not parse", bad)
		}
	}
}

func TestRemotePayload(t *testing.T) {
	want := map[string]RemotePayload{
		"play":  {DeviceID: "d1", Action: "toggle_play"},
		"back":  {DeviceID: "d1", Action: "seek", Value: -30},
		"fwd":   {DeviceID: "d1", Action: "seek", Value: 30},
		"prev":  {DeviceID: "d1", Action: "prev"},
		"next":  {DeviceID: "d1", Action: "next"},
		"mute":  {DeviceID: "d1", Action: "mute"},
		"night": {DeviceID: "d1", Action: "night"},
		"sleep": {DeviceID: "d1", Action: "sleep", Value: 30},
	}
	for k, w := range want {
		if got, ok := remotePayload("d1", k); !ok || got != w {
			t.Fatalf("%s: %+v %v", k, got, ok)
		}
	}
	if _, ok := remotePayload("d1", "explode"); ok {
		t.Fatal("unknown action accepted")
	}
	// Every remote button maps to a known action.
	for _, row := range remoteKeyboard("uk").InlineKeyboard {
		for _, btn := range row {
			cb, err := parseCallback(btn.CallbackData)
			if err != nil || cb.Kind != "rc" {
				t.Fatalf("button %+v", btn)
			}
			if _, ok := remotePayload("x", cb.Arg); !ok {
				t.Fatalf("button %q has no payload", cb.Arg)
			}
		}
	}
	b, _ := json.Marshal(want["back"])
	if string(b) != `{"device_id":"d1","action":"seek","value":-30}` {
		t.Fatalf("json: %s", b)
	}
}

func TestContinueLine(t *testing.T) {
	tv := catalog.Title{TMDBID: 1399, Type: "tv", Title: "Гра <престолів>"}
	got := continueLine(tv, store.Timecode{Season: 2, Episode: 5, PositionSec: 2530, DurationSec: 3720})
	if got != "▶ 📺 <b>Гра &lt;престолів&gt;</b> (S2E5 · 42:10 / 1:02:00)" {
		t.Fatalf("tv: %q", got)
	}
	movie := catalog.Title{TMDBID: 550, Type: "movie", Title: "Бійцівський клуб"}
	got = continueLine(movie, store.Timecode{PositionSec: 61, DurationSec: 8340})
	if got != "▶ 🎬 <b>Бійцівський клуб</b> (1:01 / 2:19:00)" {
		t.Fatalf("movie: %q", got)
	}
	if got := continueLine(catalog.Title{TMDBID: 7, Type: "movie"}, store.Timecode{}); got != "▶ 🎬 <b>#7</b>" {
		t.Fatalf("bare: %q", got)
	}
	l := &listing{kind: "continue", items: []catalog.Title{tv}, tcs: []store.Timecode{{Season: 1, Episode: 1}}}
	text, kb := renderList("en", l, 1, nil)
	if !strings.Contains(text, "Continue watching") || len(kb.InlineKeyboard) != 1 || kb.InlineKeyboard[0][0].CallbackData != "open:1399:tv:1" {
		t.Fatalf("continue list: %q %+v", text, kb.InlineKeyboard)
	}
}

func TestRenderListKeyboard(t *testing.T) {
	items := make([]catalog.Title, 7)
	for i := range items {
		items[i] = catalog.Title{TMDBID: i + 1, Type: "movie", Title: "Фільм", Year: 2000 + i}
	}
	items[1].Type = "tv"
	l := &listing{kind: "search", q: "q<", items: items}

	text, kb := renderList("uk", l, 1, map[string]bool{"tv:2": true})
	if !strings.Contains(text, "«q&lt;»") || !strings.Contains(text, "1. 🎬 <b>Фільм</b> (2000)") || !strings.Contains(text, "2. 📺 <b>Фільм</b> (2001)") {
		t.Fatalf("text: %q", text)
	}
	if len(kb.InlineKeyboard) != pageSize+1 { // 5 results + nav row
		t.Fatalf("page 1 rows = %d", len(kb.InlineKeyboard))
	}
	nav := kb.InlineKeyboard[pageSize]
	if len(nav) != 1 || nav[0].Text != "▶" || nav[0].CallbackData != "page:2" {
		t.Fatalf("page 1 nav: %+v", nav)
	}
	row := kb.InlineKeyboard[0]
	if len(row) != 2 || row[0].CallbackData != "open:1:movie:0" || row[1].Text != "☆" || row[1].CallbackData != "bm:1:movie:1" {
		t.Fatalf("row 1: %+v", row)
	}
	if kb.InlineKeyboard[1][1].Text != "★" {
		t.Fatalf("bookmarked star: %+v", kb.InlineKeyboard[1])
	}

	_, kb = renderList("uk", l, 2, nil)
	if len(kb.InlineKeyboard) != 3 { // 2 results + nav
		t.Fatalf("page 2 rows = %d", len(kb.InlineKeyboard))
	}
	if nav := kb.InlineKeyboard[2]; len(nav) != 1 || nav[0].Text != "◀" {
		t.Fatalf("page 2 nav: %+v", nav)
	}
	// hasMore: a further TMDB page exists → ▶ even at the end of the cached items.
	l.tmdbPages, l.totalPages = 1, 2
	_, kb = renderList("uk", l, 2, nil)
	if nav := kb.InlineKeyboard[2]; len(nav) != 2 {
		t.Fatalf("page 2 nav with more: %+v", nav)
	}
	// Empty page: text says so, no result rows, only ◀.
	text, kb = renderList("uk", &listing{kind: "search", q: "q"}, 2, nil)
	if !strings.Contains(text, "Нічого не знайдено") || len(kb.InlineKeyboard) != 1 {
		t.Fatalf("empty page: %q %+v", text, kb.InlineKeyboard)
	}
	// Bookmarks listing: ✕ instead of a star, localized.
	_, kb = renderList("en", &listing{kind: "bookmarks", items: items[:1]}, 1, nil)
	if kb.InlineKeyboard[0][1].Text != "✕ Remove" {
		t.Fatalf("bookmarks row: %+v", kb.InlineKeyboard[0])
	}
}

func TestDevicePickerAndSettings(t *testing.T) {
	many := []promsync.DeviceInfo{{ID: "a", Name: "Вітальня"}, {ID: "b", Name: ""}}
	kb := devicePicker(many, "uk")
	if len(kb.InlineKeyboard) != 2 || kb.InlineKeyboard[1][0].CallbackData != "dev:b" || kb.InlineKeyboard[1][0].Text != "пристрій b" {
		t.Fatalf("picker: %+v", kb.InlineKeyboard)
	}
	kb = settingsKeyboard("ru")
	if kb.InlineKeyboard[0][1].Text != "✓ 🇷🇺 Русский" || kb.InlineKeyboard[0][2].CallbackData != "lang:en" || kb.InlineKeyboard[1][0].CallbackData != "unlink:ask" {
		t.Fatalf("settings: %+v", kb.InlineKeyboard)
	}
	rows := []catalog.Row{{ID: "trending", Title: "У тренді", Items: []catalog.Title{{}}}, {ID: "empty", Title: "x"}}
	if kb = homeMenu(rows); len(kb.InlineKeyboard) != 1 || kb.InlineKeyboard[0][0].CallbackData != "home:trending" {
		t.Fatalf("home menu: %+v", kb.InlineKeyboard)
	}
}

func TestLinkCodes(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := NewLinks(func() time.Time { return now })

	code, exp := l.Issue(7)
	if len(code) != 6 || !exp.Equal(now.Add(linkTTL)) {
		t.Fatalf("issue: %q %v", code, exp)
	}
	if _, ok := l.Consume("000000"); ok && code != "000000" {
		t.Fatal("unknown code consumed")
	}
	if uid, ok := l.Consume(code); !ok || uid != 7 {
		t.Fatalf("consume: %d %v", uid, ok)
	}
	if _, ok := l.Consume(code); ok {
		t.Fatal("code must be one-shot")
	}

	code, _ = l.Issue(8)
	now = now.Add(linkTTL + time.Second)
	if _, ok := l.Consume(code); ok {
		t.Fatal("expired code consumed")
	}

	// Re-issue replaces the user's outstanding code.
	old, _ := l.Issue(9)
	fresh, _ := l.Issue(9)
	if _, ok := l.Consume(old); ok && old != fresh {
		t.Fatal("old code must be invalidated by re-issue")
	}
	if uid, ok := l.Consume(fresh); !ok || uid != 9 {
		t.Fatal("fresh code must work")
	}
}

func TestHubOnlineDevices(t *testing.T) {
	h := promsync.NewHub()
	_, c1 := h.Subscribe(1, "dev1", "TV")
	_, c2 := h.Subscribe(1, "dev1", "TV") // reconnect: same device twice
	_, c3 := h.Subscribe(1, "dev2", "Спальня")
	_, c4 := h.Subscribe(2, "other", "x")
	defer c4()
	got := h.OnlineDevices(1)
	if len(got) != 2 || got[0].ID != "dev1" || got[1].ID != "dev2" {
		t.Fatalf("online: %+v", got)
	}
	c1()
	c2()
	c3()
	if n := len(h.OnlineDevices(1)); n != 0 {
		t.Fatalf("after cancel: %d", n)
	}
}

func TestClientErrorsNeverCarryToken(t *testing.T) {
	const token = "123:SECRET"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/bot"+token+"/getMe") {
			t.Errorf("path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 429, "description": "Too Many Requests", "parameters": map[string]int{"retry_after": 3}})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, token)
	_, err := c.GetMe(context.Background())
	var ae *APIError
	if err == nil || !errors.As(err, &ae) || ae.RetryAfter != 3*time.Second {
		t.Fatalf("want APIError with retry_after, got %v", err)
	}

	// Transport failure: the *url.Error would embed the URL (and token).
	srv.Close()
	_, err = c.GetMe(context.Background())
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error leaks token: %v", err)
	}
}
