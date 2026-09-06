package telegram

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sviniabanditka/promin/server/internal/catalog"
	"github.com/sviniabanditka/promin/server/internal/store"
	promsync "github.com/sviniabanditka/promin/server/internal/sync"
)

const (
	pollTimeout   = 30 * time.Second
	callTimeout   = 15 * time.Second
	searchLang    = "uk"
	searchMinGap  = 2 * time.Second // per-chat rate limit
	maxChatStates = 1000
)

var sixDigits = regexp.MustCompile(`^\d{6}$`)

// chatState is the last search of a chat: enough to page without
// re-sending the query in callback data and to name a title when opening.
type chatState struct {
	q          string
	items      []catalog.Title
	tmdbPages  int // TMDB pages fetched so far
	totalPages int // TMDB total pages
	lastSearch time.Time
}

// OpenTitlePayload is the sync.EventOpenTitle payload.
type OpenTitlePayload struct {
	TMDBID    int    `json:"tmdb_id"`
	MediaType string `json:"media_type"`
	DeviceID  string `json:"device_id"`
	Title     string `json:"title"`
}

// Bot is the long-polling Telegram companion. Nil-safe getters let httpapi
// hold a nil *Bot when the token is unset.
type Bot struct {
	api     *Client
	links   *Links
	repo    *store.TelegramRepo
	catalog *catalog.Service
	hub     *promsync.Hub
	log     *slog.Logger

	mu       sync.Mutex
	username string
	chats    map[int64]*chatState
}

// New builds a Bot. The username is learned from getMe inside Run.
func New(api *Client, repo *store.TelegramRepo, cat *catalog.Service, hub *promsync.Hub, logger *slog.Logger) *Bot {
	return &Bot{
		api: api, links: NewLinks(nil), repo: repo, catalog: cat, hub: hub, log: logger,
		chats: map[int64]*chatState{},
	}
}

// Username is the bot's @name ("" until getMe succeeded).
func (b *Bot) Username() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.username
}

// IssueLinkCode mints a link code for userID (POST /api/v1/telegram/link).
func (b *Bot) IssueLinkCode(userID int64) (string, time.Time) { return b.links.Issue(userID) }

// IsLinked reports whether userID has a linked chat.
func (b *Bot) IsLinked(userID int64) (bool, error) { return b.repo.IsUserLinked(userID) }

// Unlink drops every chat of userID (DELETE /api/v1/telegram/link).
func (b *Bot) Unlink(userID int64) error { return b.repo.UnlinkUser(userID) }

// Run polls getUpdates until ctx is cancelled. Errors back off (1 s → 60 s).
func (b *Bot) Run(ctx context.Context) {
	backoff := time.Second
	for {
		me, err := b.api.GetMe(ctx)
		if err == nil {
			b.mu.Lock()
			b.username = me.Username
			b.mu.Unlock()
			b.log.Info("telegram: bot online", "username", me.Username)
			break
		}
		b.log.Warn("telegram: getMe failed", "error", err, "retry_in", backoff)
		if !sleep(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, time.Minute)
	}

	var offset int64
	backoff = time.Second
	for ctx.Err() == nil {
		updates, err := b.api.GetUpdates(ctx, offset, pollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var ae *APIError
			if errors.As(err, &ae) && ae.RetryAfter > 0 {
				backoff = ae.RetryAfter
			}
			b.log.Warn("telegram: getUpdates failed", "error", err, "retry_in", backoff)
			if !sleep(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, time.Minute)
			continue
		}
		backoff = time.Second
		for _, u := range updates {
			offset = max(offset, u.UpdateID+1)
			b.handle(ctx, u)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (b *Bot) handle(parent context.Context, u Update) {
	ctx, cancel := context.WithTimeout(parent, callTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("telegram: handler panic", "panic", r)
		}
	}()
	switch {
	case u.Message != nil && u.Message.Chat.Type == "private":
		b.handleMessage(ctx, u.Message)
	case u.CallbackQuery != nil && u.CallbackQuery.Message != nil && u.CallbackQuery.Message.Chat.Type == "private":
		b.handleCallback(ctx, u.CallbackQuery)
	}
}

const (
	textUnlinked = "Цей чат не прив'язано. Відкрий Promin → Налаштування → Telegram і надішли мені код."
	textHelp     = "Надішли назву фільму або серіалу — я знайду і відкрию на телевізорі.\n\n/unlink — відв'язати цей чат\n/help — ця підказка"
)

func (b *Bot) handleMessage(ctx context.Context, m *Message) {
	chatID := m.Chat.ID
	text := strings.TrimSpace(m.Text)
	cmd, arg, _ := strings.Cut(text, " ")
	arg = strings.TrimSpace(arg)

	switch {
	case cmd == "/start" && sixDigits.MatchString(arg), sixDigits.MatchString(text):
		if arg == "" {
			arg = text
		}
		b.link(ctx, chatID, arg)
		return
	case cmd == "/unlink":
		if err := b.repo.UnlinkChat(chatID); err != nil {
			b.fail(ctx, chatID, "unlink", err)
			return
		}
		b.forget(chatID)
		b.reply(ctx, chatID, "Чат відв'язано від Promin.")
		return
	}

	userID, err := b.repo.UserByChat(chatID)
	if errors.Is(err, store.ErrNotFound) {
		b.reply(ctx, chatID, textUnlinked)
		return
	}
	if err != nil {
		b.fail(ctx, chatID, "lookup link", err)
		return
	}

	switch cmd {
	case "/start":
		b.reply(ctx, chatID, "Привіт! "+textHelp)
	case "/help":
		b.reply(ctx, chatID, textHelp)
	default:
		if text == "" || strings.HasPrefix(text, "/") {
			b.reply(ctx, chatID, textHelp)
			return
		}
		b.search(ctx, chatID, userID, text)
	}
}

func (b *Bot) link(ctx context.Context, chatID int64, code string) {
	userID, ok := b.links.Consume(code)
	if !ok {
		b.reply(ctx, chatID, "Код невірний або застарів. Отримай новий у Promin → Налаштування → Telegram.")
		return
	}
	if err := b.repo.Link(chatID, userID, time.Now().Unix()); err != nil {
		b.fail(ctx, chatID, "link", err)
		return
	}
	b.log.Info("telegram: chat linked", "user_id", userID)
	b.reply(ctx, chatID, "✅ Готово, чат прив'язано до твого профілю Promin. "+textHelp)
}

func (b *Bot) search(ctx context.Context, chatID, userID int64, q string) {
	b.mu.Lock()
	if len(b.chats) > maxChatStates { // ponytail: flush all instead of LRU; states are cheap to rebuild
		b.chats = map[int64]*chatState{}
	}
	st := b.chats[chatID]
	if st != nil && time.Since(st.lastSearch) < searchMinGap {
		b.mu.Unlock()
		b.reply(ctx, chatID, "Не так швидко — зачекай секунду.")
		return
	}
	st = &chatState{q: q, lastSearch: time.Now()}
	b.chats[chatID] = st
	b.mu.Unlock()

	if err := b.fetchPage(ctx, st, 1); err != nil {
		b.fail(ctx, chatID, "search", err)
		return
	}
	b.mu.Lock()
	text, markup := searchPage(q, st.items, 1, st.tmdbPages < st.totalPages)
	b.mu.Unlock()
	b.reply(ctx, chatID, text, markup)
}

// fetchPage appends TMDB page n to st (no-op if already fetched). Caller must
// not hold b.mu; st fields are written under it.
func (b *Bot) fetchPage(ctx context.Context, st *chatState, n int) error {
	b.mu.Lock()
	done := n <= st.tmdbPages
	b.mu.Unlock()
	if done {
		return nil
	}
	res, err := b.catalog.Search(ctx, st.q, searchLang, n)
	if err != nil {
		return err
	}
	b.mu.Lock()
	st.items = append(st.items, res.Items...)
	st.tmdbPages, st.totalPages = n, res.TotalPages
	b.mu.Unlock()
	return nil
}

func (b *Bot) handleCallback(ctx context.Context, cq *CallbackQuery) {
	chatID := cq.Message.Chat.ID
	cb, err := parseCallback(cq.Data)
	if err != nil {
		_ = b.api.AnswerCallback(ctx, cq.ID, "Незрозуміла кнопка")
		return
	}
	userID, err := b.repo.UserByChat(chatID)
	if err != nil {
		_ = b.api.AnswerCallback(ctx, cq.ID, "Спочатку прив'яжи чат до Promin")
		return
	}

	switch cb.Kind {
	case "page":
		b.mu.Lock()
		st := b.chats[chatID]
		stale := st == nil || queryHash(st.q) != cb.QueryHash
		b.mu.Unlock()
		if stale {
			_ = b.api.AnswerCallback(ctx, cq.ID, "Цей пошук застарів — надішли запит ще раз")
			return
		}
		need := cb.Page * pageSize
		b.mu.Lock()
		next := st.tmdbPages + 1
		more := len(st.items) < need && st.tmdbPages < st.totalPages
		b.mu.Unlock()
		if more {
			if err := b.fetchPage(ctx, st, next); err != nil {
				_ = b.api.AnswerCallback(ctx, cq.ID, "Каталог недоступний, спробуй пізніше")
				return
			}
		}
		b.mu.Lock()
		text, markup := searchPage(st.q, st.items, cb.Page, st.tmdbPages < st.totalPages)
		b.mu.Unlock()
		_ = b.api.AnswerCallback(ctx, cq.ID, "")
		if err := b.api.EditMessage(ctx, chatID, cq.Message.MessageID, text, markup); err != nil {
			b.log.Debug("telegram: edit failed", "error", err)
		}

	case "open":
		target, text, markup := openReply(b.hub.OnlineDevices(userID), cb.TMDBID, cb.MediaType)
		if target != nil {
			b.publishOpen(userID, chatID, *target, cb.TMDBID, cb.MediaType)
		}
		_ = b.api.AnswerCallback(ctx, cq.ID, "")
		b.reply(ctx, chatID, text, markup)

	case "dev":
		var target *promsync.DeviceInfo
		for _, d := range b.hub.OnlineDevices(userID) {
			if d.ID == cb.DeviceID {
				target = &d
				break
			}
		}
		if target == nil {
			_ = b.api.AnswerCallback(ctx, cq.ID, "Пристрій уже офлайн")
			return
		}
		b.publishOpen(userID, chatID, *target, cb.TMDBID, cb.MediaType)
		_ = b.api.AnswerCallback(ctx, cq.ID, "")
		// Replace the chooser (and its keyboard) with the outcome.
		if err := b.api.EditMessage(ctx, chatID, cq.Message.MessageID, "Відкрито на "+deviceLabel(*target), nil); err != nil {
			b.reply(ctx, chatID, "Відкрито на "+deviceLabel(*target))
		}
	}
}

func (b *Bot) publishOpen(userID, chatID int64, dev promsync.DeviceInfo, tmdbID int, mediaType string) {
	b.mu.Lock()
	title := ""
	if st := b.chats[chatID]; st != nil {
		for _, t := range st.items {
			if t.TMDBID == tmdbID && t.Type == mediaType {
				title = t.Title
				break
			}
		}
	}
	b.mu.Unlock()
	b.hub.Publish(userID, promsync.EventOpenTitle, OpenTitlePayload{TMDBID: tmdbID, MediaType: mediaType, DeviceID: dev.ID, Title: title})
	b.log.Info("telegram: open title", "user_id", userID, "tmdb_id", tmdbID, "media_type", mediaType, "device", dev.Name)
}

func (b *Bot) forget(chatID int64) {
	b.mu.Lock()
	delete(b.chats, chatID)
	b.mu.Unlock()
}

func (b *Bot) reply(ctx context.Context, chatID int64, text string, markup ...*InlineKeyboardMarkup) {
	var m *InlineKeyboardMarkup
	if len(markup) > 0 {
		m = markup[0]
	}
	if err := b.api.SendMessage(ctx, chatID, text, m); err != nil {
		b.log.Warn("telegram: send failed", "error", err)
	}
}

func (b *Bot) fail(ctx context.Context, chatID int64, what string, err error) {
	b.log.Warn("telegram: "+what+" failed", "error", err)
	b.reply(ctx, chatID, "Щось пішло не так, спробуй ще раз пізніше.")
}
