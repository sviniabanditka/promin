package httpapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/sviniabanditka/promin/server/internal/auth"
	"github.com/sviniabanditka/promin/server/internal/store"
)

func authFixture(t *testing.T) (*auth.Service, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	u, err := db.Users.Create("tv", "x", 1)
	if err != nil {
		t.Fatal(err)
	}
	const tok = "valid-token-123"
	if err := db.Sessions.Create(store.Session{Token: auth.HashToken(tok), ID: auth.TokenID(tok), UserID: u.ID, DeviceType: "tv", CreatedAt: 1, LastSeen: 1}); err != nil {
		t.Fatal(err)
	}
	return auth.NewService(db, auth.Config{}), tok
}

func okHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := authFrom(r); !ok {
		http.Error(w, "no auth info in ctx", http.StatusTeapot)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// The hard gate: API routes take the token from the Authorization header ONLY;
// media routes (<video src>, <img>) may also take ?t=. Anything else is 401.
func TestAuthMiddlewareHeaderVsQuery(t *testing.T) {
	svc, tok := authFixture(t)
	api := requireAuth(svc, okHandler)
	media := requireAuthMedia(svc, okHandler)

	run := func(h http.HandlerFunc, target, bearer string) int {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	if c := run(api, "/x", tok); c != http.StatusNoContent {
		t.Errorf("api + header: %d", c)
	}
	if c := run(api, "/x?t="+tok, ""); c != http.StatusUnauthorized {
		t.Errorf("api must NOT accept ?t=: %d", c)
	}
	if c := run(api, "/x", ""); c != http.StatusUnauthorized {
		t.Errorf("api without token: %d", c)
	}
	if c := run(api, "/x", "bogus"); c != http.StatusUnauthorized {
		t.Errorf("api bogus token: %d", c)
	}
	if c := run(media, "/relay?u=a&t="+tok, ""); c != http.StatusNoContent {
		t.Errorf("media + ?t=: %d", c)
	}
	if c := run(media, "/relay?u=a", tok); c != http.StatusNoContent {
		t.Errorf("media + header: %d", c)
	}
	if c := run(media, "/relay?u=a", ""); c != http.StatusUnauthorized {
		t.Errorf("media without token: %d", c)
	}
}
