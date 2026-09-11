package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sviniabanditka/promin/server/internal/auth"
	"github.com/sviniabanditka/promin/server/internal/store"
	"github.com/sviniabanditka/promin/server/internal/sync"
	"github.com/sviniabanditka/promin/server/internal/telegram"
)

// tgFixture: one profile with a TV session and a phone (Mini App) session,
// the TV subscribed to the hub. Returns the handlers, the phone token and the
// TV's device id.
func tgFixture(t *testing.T) (*tgAppHandlers, http.Handler, string, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "tg.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	u, err := db.Users.Create("family", "x", 1)
	if err != nil {
		t.Fatal(err)
	}
	const tv, phone = "tv-token-000000000000", "phone-token-0000000000"
	for _, s := range []store.Session{
		{Token: tv, UserID: u.ID, DeviceName: "Living room", DeviceType: "tizen", CreatedAt: 1, LastSeen: 1},
		{Token: phone, UserID: u.ID, DeviceName: "Kostia · Telegram", DeviceType: "telegram", CreatedAt: 2, LastSeen: 2},
	} {
		s.ID, s.Token = auth.TokenID(s.Token), auth.HashToken(s.Token) // stored form
		if err := db.Sessions.Create(s); err != nil {
			t.Fatal(err)
		}
	}
	hub := sync.NewHub()
	_, cancel := hub.Subscribe(u.ID, auth.TokenID(tv), "Living room")
	t.Cleanup(cancel)
	authSvc := auth.NewService(db, auth.Config{})
	h := &tgAppHandlers{auth: authSvc, sync: sync.NewService(db, hub)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/tg/auth", h.login)
	mux.HandleFunc("GET /api/v1/tg/devices", requireAuth(authSvc, h.devices))
	mux.HandleFunc("POST /api/v1/tg/send", requireAuth(authSvc, h.send))
	mux.HandleFunc("POST /api/v1/player/state", requireAuth(authSvc, (&playerStateHandlers{hub: hub}).set))
	return h, mux, phone, auth.TokenID(tv)
}

func tgCall(h http.Handler, method, target, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestTGDevicesShape(t *testing.T) {
	_, mux, phone, tvID := tgFixture(t)

	// TV reports a state first; the phone lists devices.
	if rr := tgCall(mux, http.MethodPost, "/api/v1/player/state", "tv-token-000000000000", `{"tmdb_id":1399,"media_type":"tv","title":"GoT","season":2,"episode":5,"position_sec":2530.4,"duration_sec":3720,"paused":false,"source":"collaps","voice":"LostFilm"}`); rr.Code != http.StatusNoContent {
		t.Fatalf("player/state: %d %s", rr.Code, rr.Body)
	}
	if rr := tgCall(mux, http.MethodPost, "/api/v1/player/state", "tv-token-000000000000", `{"media_type":"tv"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("player/state without tmdb_id: %d", rr.Code)
	}
	rr := tgCall(mux, http.MethodGet, "/api/v1/tg/devices", phone, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("devices: %d %s", rr.Code, rr.Body)
	}
	var out struct {
		Devices []struct {
			ID      string            `json:"id"`
			Name    string            `json:"name"`
			Type    string            `json:"type"`
			Current bool              `json:"current"`
			Online  bool              `json:"online"`
			State   *sync.PlayerState `json:"state"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Devices) != 2 {
		t.Fatalf("devices: %+v", out.Devices)
	}
	byID := map[string]int{}
	for i, d := range out.Devices {
		byID[d.ID] = i
	}
	tv := out.Devices[byID[tvID]]
	if !tv.Online || tv.Current || tv.Name != "Living room" || tv.Type != "tizen" || tv.State == nil || tv.State.TMDBID != 1399 || tv.State.Episode != 5 || tv.State.UpdatedAt == 0 {
		t.Fatalf("tv: %+v state %+v", tv, tv.State)
	}
	ph := out.Devices[byID[auth.TokenID(phone)]]
	if ph.Online || !ph.Current || ph.State != nil || ph.Type != "telegram" {
		t.Fatalf("phone: %+v", ph)
	}

	// Closing drops the state.
	tgCall(mux, http.MethodPost, "/api/v1/player/state", "tv-token-000000000000", `{"closed":true}`)
	rr = tgCall(mux, http.MethodGet, "/api/v1/tg/devices", phone, "")
	if !strings.Contains(rr.Body.String(), `"state":null`) {
		t.Fatalf("state after close: %s", rr.Body)
	}
}

func TestTGSendValidation(t *testing.T) {
	h, mux, phone, tvID := tgFixture(t)
	hub := h.sync.Hub()
	// Second subscriber to capture what the handler publishes.
	ch, cancel := hub.Subscribe(1, "watcher", "w")
	defer cancel()

	cases := []struct {
		body string
		want int
	}{
		{`not json`, 400},
		{`{"device_id":"` + tvID + `"}`, 400},
		{`{"device_id":"` + tvID + `","open":{"tmdb_id":1},"remote":{"action":"next"}}`, 400},
		{`{"open":{"tmdb_id":550,"media_type":"movie"}}`, 400},
		{`{"device_id":"` + tvID + `","open":{"tmdb_id":0,"media_type":"movie"}}`, 400},
		{`{"device_id":"` + tvID + `","open":{"tmdb_id":550,"media_type":"book"}}`, 400},
		{`{"device_id":"` + tvID + `","remote":{"action":"explode"}}`, 400},
		{`{"device_id":"nope00000000","remote":{"action":"next"}}`, 404},
		{`{"device_id":"` + tvID + `","remote":{"action":"seek_to","value":1234.5}}`, 204},
		{`{"device_id":"` + tvID + `","open":{"tmdb_id":1399,"media_type":"tv","season":2,"episode":6}}`, 204},
		{`{"device_id":"` + tvID + `","open":{"tmdb_id":550,"media_type":"movie","resume":true}}`, 204},
	}
	for _, c := range cases {
		if rr := tgCall(mux, http.MethodPost, "/api/v1/tg/send", phone, c.body); rr.Code != c.want {
			t.Errorf("%s → %d (want %d) %s", c.body, rr.Code, c.want, rr.Body)
		}
	}

	var got []sync.Event
	for len(ch) > 0 {
		got = append(got, <-ch)
	}
	if len(got) != 3 {
		t.Fatalf("published %d events, want 3", len(got))
	}
	js := func(ev sync.Event) string { b, _ := json.Marshal(ev.Payload); return string(b) }
	if got[0].Type != sync.EventRemote || js(got[0]) != `{"device_id":"`+tvID+`","action":"seek_to","value":1234.5}` {
		t.Fatalf("remote: %s %s", got[0].Type, js(got[0]))
	}
	if got[1].Type != sync.EventOpenTitle || js(got[1]) != `{"tmdb_id":1399,"media_type":"tv","device_id":"`+tvID+`","title":"","season":2,"episode":6}` {
		t.Fatalf("open episode: %s %s", got[1].Type, js(got[1]))
	}
	if js(got[2]) != `{"tmdb_id":550,"media_type":"movie","device_id":"`+tvID+`","title":"","resume":true}` {
		t.Fatalf("open resume: %s", js(got[2]))
	}
	if _, ok := got[2].Payload.(telegram.OpenTitlePayload); !ok {
		t.Fatal("payload type")
	}
}

func TestTGAuthDisabled(t *testing.T) {
	_, mux, _, _ := tgFixture(t) // bot == nil
	rr := tgCall(mux, http.MethodPost, "/api/v1/tg/auth", "", `{"init_data":"x"}`)
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "telegram_disabled") {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
}

// /tg/ is a second SPA: its own index fallback, root SPA untouched.
func TestStaticTGSection(t *testing.T) {
	root := fstest.MapFS{
		"index.html":    {Data: []byte("root")},
		"app.js":        {Data: []byte("root js")},
		"tg/index.html": {Data: []byte("tg")},
		"tg/app.js":     {Data: []byte("tg js")},
	}
	h := spaHandler(root)
	body := func(target string) (int, string, string) {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
		return rr.Code, rr.Body.String(), rr.Header().Get("Cache-Control")
	}
	if c, b, cc := body("/tg/"); c != 200 || b != "tg" || cc != "no-cache" {
		t.Fatalf("/tg/: %d %q %q", c, b, cc)
	}
	if _, b, _ := body("/tg/app.js"); b != "tg js" {
		t.Fatalf("/tg/app.js: %q", b)
	}
	if _, b, _ := body("/tg/title/550"); b != "tg" {
		t.Fatalf("/tg/ deep link: %q", b)
	}
	if _, b, _ := body("/title/550"); b != "root" {
		t.Fatalf("root deep link: %q", b)
	}
	if c, _, _ := body("/tg"); c != http.StatusMovedPermanently {
		t.Fatalf("/tg redirect: %d", c)
	}
	// No Mini App build yet: /tg/ falls back to the root SPA instead of 404.
	delete(root, "tg/index.html")
	if _, b, _ := body("/tg/"); b != "root" {
		t.Fatalf("/tg/ without build: %q", b)
	}
}
