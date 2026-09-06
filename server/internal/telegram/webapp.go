package telegram

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sviniabanditka/promin/server/internal/store"
)

// initDataMaxAge: Mini App initData older than this is refused
// (docs/miniapp.md).
const initDataMaxAge = 24 * time.Hour

var (
	// ErrInitData is a bad/expired Telegram.WebApp.initData (401 tg_invalid).
	ErrInitData = errors.New("telegram: invalid init data")
	// ErrNotLinked means the Telegram user has no linked profile (403 tg_not_linked).
	ErrNotLinked = errors.New("telegram: chat not linked")
)

// WebAppUser is the "user" field of initData.
type WebAppUser struct {
	ID           int64  `json:"id"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
}

// ValidateInitData checks initData per Telegram's spec: HMAC-SHA256 of the
// sorted "key=value" lines (all fields but hash) keyed by
// HMAC-SHA256("WebAppData", botToken), and auth_date within initDataMaxAge
// of now. It returns the embedded user.
func ValidateInitData(initData, botToken string, now time.Time) (WebAppUser, error) {
	q, err := url.ParseQuery(initData)
	if err != nil || q.Get("hash") == "" {
		return WebAppUser{}, ErrInitData
	}
	hash := q.Get("hash")
	q.Del("hash")
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+q.Get(k))
	}
	if !hmac.Equal([]byte(signInitData(strings.Join(lines, "\n"), botToken)), []byte(hash)) {
		return WebAppUser{}, ErrInitData
	}
	authDate, err := strconv.ParseInt(q.Get("auth_date"), 10, 64)
	if err != nil || now.Sub(time.Unix(authDate, 0)) > initDataMaxAge {
		return WebAppUser{}, ErrInitData
	}
	var u WebAppUser
	if err := json.Unmarshal([]byte(q.Get("user")), &u); err != nil || u.ID == 0 {
		return WebAppUser{}, ErrInitData
	}
	return u, nil
}

// signInitData is the hex HMAC of a data-check-string (shared with the test).
func signInitData(dataCheck, botToken string) string {
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(dataCheck))
	return hex.EncodeToString(mac.Sum(nil))
}

// Auth validates a Mini App's initData and resolves the linked profile
// (POST /api/v1/tg/auth). A private chat's id equals the user's id, so the
// telegram_links lookup is by user.id.
func (b *Bot) Auth(initData string) (userID int64, u WebAppUser, err error) {
	u, err = ValidateInitData(initData, b.api.token, time.Now())
	if err != nil {
		return 0, u, err
	}
	userID, err = b.repo.UserByChat(u.ID)
	if errors.Is(err, store.ErrNotFound) {
		return 0, u, ErrNotLinked
	}
	return userID, u, err
}
