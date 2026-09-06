package telegram

import (
	"net/url"
	"strconv"
	"testing"
	"time"
)

const testBotToken = "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"

// signedInitData builds initData the way Telegram does, signed with token.
func signedInitData(t *testing.T, token string, authDate time.Time, user string) string {
	t.Helper()
	fields := map[string]string{
		"auth_date": strconv.FormatInt(authDate.Unix(), 10),
		"query_id":  "AAHdF6IQAAAAAN0XohDhrOrc",
		"user":      user,
	}
	// Data-check-string: sorted keys, "k=v" joined by \n (raw values).
	dataCheck := "auth_date=" + fields["auth_date"] + "\nquery_id=" + fields["query_id"] + "\nuser=" + fields["user"]
	q := url.Values{}
	for k, v := range fields {
		q.Set(k, v)
	}
	q.Set("hash", signInitData(dataCheck, token))
	return q.Encode()
}

func TestValidateInitData(t *testing.T) {
	now := time.Unix(1_788_800_000, 0)
	user := `{"id":279058397,"first_name":"Vladislav","last_name":"Kibenko","username":"vdkfrost","language_code":"ru","is_premium":true}`

	u, err := ValidateInitData(signedInitData(t, testBotToken, now.Add(-time.Hour), user), testBotToken, now)
	if err != nil {
		t.Fatalf("valid: %v", err)
	}
	if u.ID != 279058397 || u.FirstName != "Vladislav" || u.LanguageCode != "ru" {
		t.Fatalf("user: %+v", u)
	}

	if _, err := ValidateInitData(signedInitData(t, "other-token", now, user), testBotToken, now); err != ErrInitData {
		t.Fatalf("bad hash: %v", err)
	}
	if _, err := ValidateInitData(signedInitData(t, testBotToken, now.Add(-25*time.Hour), user), testBotToken, now); err != ErrInitData {
		t.Fatalf("expired: %v", err)
	}
	// Tampered field after signing.
	tampered := signedInitData(t, testBotToken, now, user) + "&extra=1"
	if _, err := ValidateInitData(tampered, testBotToken, now); err != ErrInitData {
		t.Fatalf("tampered: %v", err)
	}
	for _, bad := range []string{"", "hash=abc", "auth_date=1&user=%7B%7D", "%zz"} {
		if _, err := ValidateInitData(bad, testBotToken, now); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}
