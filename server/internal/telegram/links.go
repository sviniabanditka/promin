package telegram

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sync"
	"time"
)

// linkTTL is how long a link code stays valid.
const linkTTL = 10 * time.Minute

type linkCode struct {
	userID  int64
	expires time.Time
}

// Links holds the in-memory one-shot link codes (POST /api/v1/telegram/link
// issues one, the bot consumes it). Codes die with the process, which is
// fine for a 10-minute pairing step.
type Links struct {
	mu    sync.Mutex
	now   func() time.Time
	codes map[string]linkCode
}

func NewLinks(now func() time.Time) *Links {
	if now == nil {
		now = time.Now
	}
	return &Links{now: now, codes: map[string]linkCode{}}
}

// Issue mints a fresh 6-digit code for userID, replacing any code the user
// still had outstanding.
func (l *Links) Issue(userID int64) (code string, expires time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for c, lc := range l.codes {
		if lc.userID == userID || !now.Before(lc.expires) {
			delete(l.codes, c)
		}
	}
	for {
		code = randomCode()
		if _, taken := l.codes[code]; !taken {
			break
		}
	}
	expires = now.Add(linkTTL)
	l.codes[code] = linkCode{userID: userID, expires: expires}
	return code, expires
}

// Consume redeems code: it returns the bound user once, then the code is gone.
func (l *Links) Consume(code string) (userID int64, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lc, found := l.codes[code]
	if !found {
		return 0, false
	}
	delete(l.codes, code)
	if !l.now().Before(lc.expires) {
		return 0, false
	}
	return lc.userID, true
}

func randomCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		panic(err) // crypto/rand failing is not something to recover from
	}
	return fmt.Sprintf("%06d", n.Int64())
}
