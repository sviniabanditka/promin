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
	promsync "github.com/sviniabanditka/promin/server/internal/sync"
)

func TestCallbackRoundTrip(t *testing.T) {
	for _, c := range []callback{
		{Kind: "open", TMDBID: 550, MediaType: "movie", Page: 2},
		{Kind: "page", QueryHash: "deadbeef", Page: 3},
		{Kind: "dev", DeviceID: "abcDEF123456", TMDBID: 1399, MediaType: "tv"},
	} {
		s := c.String()
		if len(s) > 64 {
			t.Fatalf("%q exceeds 64 bytes", s)
		}
		got, err := parseCallback(s)
		if err != nil || got != c {
			t.Fatalf("roundtrip %q: %+v err=%v", s, got, err)
		}
	}
	for _, bad := range []string{"", "open:x:movie:1", "open:1:book:1", "page:h", "nope:1", "dev:d:1:person"} {
		if _, err := parseCallback(bad); err == nil {
			t.Fatalf("%q must not parse", bad)
		}
	}
}

func TestSearchPageKeyboard(t *testing.T) {
	items := make([]catalog.Title, 7)
	for i := range items {
		items[i] = catalog.Title{TMDBID: i + 1, Type: "movie", Title: "Фільм", Year: 2000 + i}
	}
	items[1].Type = "tv"

	text, kb := searchPage("q", items, 1, false)
	if !strings.Contains(text, "1. 🎬 Фільм (2000)") || !strings.Contains(text, "2. 📺 Фільм (2001)") {
		t.Fatalf("text: %q", text)
	}
	if len(kb.InlineKeyboard) != pageSize+1 { // 5 results + nav row
		t.Fatalf("page 1 rows = %d", len(kb.InlineKeyboard))
	}
	nav := kb.InlineKeyboard[pageSize]
	if len(nav) != 1 || nav[0].Text != "▶" || nav[0].CallbackData != "page:"+queryHash("q")+":2" {
		t.Fatalf("page 1 nav: %+v", nav)
	}
	if kb.InlineKeyboard[0][0].CallbackData != "open:1:movie:1" {
		t.Fatalf("open button: %+v", kb.InlineKeyboard[0][0])
	}

	_, kb = searchPage("q", items, 2, false)
	if len(kb.InlineKeyboard) != 3 { // 2 results + nav
		t.Fatalf("page 2 rows = %d", len(kb.InlineKeyboard))
	}
	if nav := kb.InlineKeyboard[2]; len(nav) != 1 || nav[0].Text != "◀" {
		t.Fatalf("page 2 nav: %+v", nav)
	}
	// hasMore: a further TMDB page exists → ▶ even at the end of the cached items.
	_, kb = searchPage("q", items, 2, true)
	if nav := kb.InlineKeyboard[2]; len(nav) != 2 {
		t.Fatalf("page 2 nav with more: %+v", nav)
	}
	// Empty page: text says so, no result rows, only ◀.
	text, kb = searchPage("q", nil, 2, false)
	if !strings.Contains(text, "Нічого не знайдено") || len(kb.InlineKeyboard) != 1 {
		t.Fatalf("empty page: %q %+v", text, kb.InlineKeyboard)
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

func TestOpenReplyDecision(t *testing.T) {
	target, text, kb := openReply(nil, 1, "movie")
	if target != nil || kb != nil || !strings.Contains(text, "Немає пристроїв") {
		t.Fatalf("none: %v %q %v", target, text, kb)
	}
	one := []promsync.DeviceInfo{{ID: "a", Name: "Вітальня"}}
	target, text, kb = openReply(one, 1, "movie")
	if target == nil || target.ID != "a" || kb != nil || text != "Відкрито на Вітальня" {
		t.Fatalf("one: %v %q %v", target, text, kb)
	}
	many := []promsync.DeviceInfo{{ID: "a", Name: "Вітальня"}, {ID: "b", Name: ""}}
	target, _, kb = openReply(many, 42, "tv")
	if target != nil || kb == nil || len(kb.InlineKeyboard) != 2 {
		t.Fatalf("many: %v %v", target, kb)
	}
	if kb.InlineKeyboard[1][0].CallbackData != "dev:b:42:tv" || kb.InlineKeyboard[1][0].Text != "пристрій b" {
		t.Fatalf("many button: %+v", kb.InlineKeyboard[1][0])
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
