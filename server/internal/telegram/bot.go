package telegram

import (
	"context"
	"errors"
	"html"
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
	searchMinGap  = 2 * time.Second // per-chat rate limit
	maxChatStates = 1000
	maxListings   = 8               // listings remembered per chat (by message id)
	deviceMemory  = 5 * time.Minute // how long a picked device is reused without asking
	continueLimit = 10
)

var sixDigits = regexp.MustCompile(`^\d{6}$`)

// action is a "send to device" request waiting for a device choice.
type action struct {
	kind      string // open | remote
	tmdbID    int
	mediaType string
	title     string
	resume    bool
	remote    string // rc key
}

// chatState is a chat's per-message listings, its pending device action and
// the last device it picked.
type chatState struct {
	lastSearch time.Time
	lists      map[int64]*listing
	order      []int64 // message ids oldest first, for eviction
	pending    *action
	lastDev    string
	lastDevAt  time.Time
}

// OpenTitlePayload is the sync.EventOpenTitle payload.
type OpenTitlePayload struct {
	TMDBID    int    `json:"tmdb_id"`
	MediaType string `json:"media_type"`
	DeviceID  string `json:"device_id"`
	Title     string `json:"title"`
	Resume    bool   `json:"resume,omitempty"` // continue from the saved timecode
	Season    int    `json:"season,omitempty"` // Mini App: start this episode
	Episode   int    `json:"episode,omitempty"`
}

// Bot is the long-polling Telegram companion. Nil-safe getters let httpapi
// hold a nil *Bot when the token is unset.
type Bot struct {
	api     *Client
	links   *Links
	repo    *store.TelegramRepo
	catalog *catalog.Service
	sync    *promsync.Service
	hub     *promsync.Hub
	log     *slog.Logger

	mu       sync.Mutex
	username string
	chats    map[int64]*chatState
}

// New builds a Bot. The username is learned from getMe inside Run.
//
// The chat menu button (the one that opens the Mini App) is deliberately left
// alone: it belongs to the bot owner and is set once in BotFather. Calling
// setChatMenuButton on every start would overwrite whatever they configured.
func New(api *Client, repo *store.TelegramRepo, cat *catalog.Service, syncSvc *promsync.Service, logger *slog.Logger) *Bot {
	return &Bot{
		api: api, links: NewLinks(nil), repo: repo, catalog: cat, sync: syncSvc, hub: syncSvc.Hub(), log: logger,
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
	// "/" menu: default list in uk, localized copies for ru/en clients.
	for _, l := range append([]string{""}, langs...) {
		if err := b.api.SetMyCommands(ctx, botCommands(normLang(l)), l); err != nil {
			b.log.Warn("telegram: setMyCommands failed", "lang", l, "error", err)
		}
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

// langOf is the profile's synced "lang" setting (uk when unset).
func (b *Bot) langOf(userID int64) string {
	s, err := b.sync.GetSettings(userID)
	if err != nil {
		return defaultLang
	}
	return normLang(s["lang"])
}

func (b *Bot) handleMessage(ctx context.Context, m *Message) {
	chatID := m.Chat.ID
	text := strings.TrimSpace(m.Text)
	cmd, arg, _ := strings.Cut(text, " ")
	arg = strings.TrimSpace(arg)

	// Before the chat is linked the only language hint is Telegram's.
	lang := defaultLang
	if m.From != nil {
		lang = normLang(m.From.LanguageCode)
	}
	if (cmd == "/start" && sixDigits.MatchString(arg)) || sixDigits.MatchString(text) {
		if arg == "" {
			arg = text
		}
		b.link(ctx, chatID, m.From, arg, lang)
		return
	}

	userID, err := b.repo.UserByChat(chatID)
	if errors.Is(err, store.ErrNotFound) {
		b.reply(ctx, chatID, tr(lang, "unlinked"), nil)
		return
	}
	if err != nil {
		b.fail(ctx, chatID, lang, "lookup link", err)
		return
	}
	lang = b.langOf(userID)

	switch cmd {
	case "/start", "/menu":
		b.reply(ctx, chatID, tr(lang, "hello")+" "+tr(lang, "help"), mainMenu(lang))
	case "/help":
		b.reply(ctx, chatID, tr(lang, "help"), mainMenu(lang))
	case "/unlink":
		b.reply(ctx, chatID, tr(lang, "unlink.confirm"), unlinkConfirm(lang))
	default:
		if text == "" || strings.HasPrefix(text, "/") {
			b.reply(ctx, chatID, tr(lang, "help"), mainMenu(lang))
			return
		}
		b.menu(ctx, chatID, userID, lang, text)
	}
}

// menu dispatches a main-menu press; anything else is a title search.
func (b *Bot) menu(ctx context.Context, chatID, userID int64, lang, text string) {
	switch menuAction(text) {
	case "search":
		b.reply(ctx, chatID, tr(lang, "search.prompt"), nil)
	case "continue":
		b.showContinue(ctx, chatID, userID, lang)
	case "bookmarks":
		b.showBookmarks(ctx, chatID, userID, lang)
	case "watch":
		home, err := b.catalog.Home(ctx, lang)
		if err != nil {
			b.fail(ctx, chatID, lang, "home", err)
			return
		}
		b.reply(ctx, chatID, tr(lang, "watch.header"), homeMenu(home.Rows))
	case "remote":
		b.reply(ctx, chatID, tr(lang, "remote.header"), remoteKeyboard(lang))
	case "settings":
		b.reply(ctx, chatID, tr(lang, "settings.header"), settingsKeyboard(lang))
	default:
		b.search(ctx, chatID, userID, lang, text)
	}
}

func (b *Bot) link(ctx context.Context, chatID int64, from *User, code, lang string) {
	userID, ok := b.links.Consume(code)
	if !ok {
		b.reply(ctx, chatID, tr(lang, "link.bad"), nil)
		return
	}
	first, uname := "", ""
	if from != nil {
		first, uname = from.FirstName, from.Username
	}
	if err := b.repo.Link(chatID, userID, first, uname, time.Now().Unix()); err != nil {
		b.fail(ctx, chatID, lang, "link", err)
		return
	}
	b.log.Info("telegram: chat linked", "user_id", userID)
	lang = b.langOf(userID)
	b.reply(ctx, chatID, tr(lang, "link.ok")+"\n\n"+tr(lang, "help"), mainMenu(lang))
}

// --- listings -----------------------------------------------------------------

func (b *Bot) state(chatID int64) *chatState {
	if len(b.chats) > maxChatStates { // ponytail: flush all instead of LRU; states are cheap to rebuild
		b.chats = map[int64]*chatState{}
	}
	st := b.chats[chatID]
	if st == nil {
		st = &chatState{lists: map[int64]*listing{}}
		b.chats[chatID] = st
	}
	return st
}

func (b *Bot) listingOf(chatID, msgID int64) *listing {
	b.mu.Lock()
	defer b.mu.Unlock()
	if st := b.chats[chatID]; st != nil {
		return st.lists[msgID]
	}
	return nil
}

func (b *Bot) remember(chatID, msgID int64, l *listing) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.state(chatID)
	if _, ok := st.lists[msgID]; !ok {
		st.order = append(st.order, msgID)
		for len(st.order) > maxListings {
			delete(st.lists, st.order[0])
			st.order = st.order[1:]
		}
	}
	st.lists[msgID] = l
}

// bookmarkSet is the user's bookmarks keyed by bmKey.
func (b *Bot) bookmarkSet(userID int64) map[string]bool {
	set := map[string]bool{}
	bms, err := b.sync.ListBookmarks(userID)
	if err != nil {
		b.log.Warn("telegram: list bookmarks failed", "error", err)
	}
	for _, bm := range bms {
		set[bmKey(bm.MediaType, int(bm.TMDBID))] = true
	}
	return set
}

// card resolves a display card, cache first; a miss falls back to "#id".
func (b *Bot) card(ctx context.Context, mediaType string, tmdbID int, lang string) catalog.Title {
	if t, ok := b.catalog.CardCached(mediaType, tmdbID, lang); ok {
		return t
	}
	t, err := b.catalog.Card(ctx, mediaType, tmdbID, lang)
	if err != nil {
		b.log.Debug("telegram: card failed", "tmdb_id", tmdbID, "error", err)
		return catalog.Title{TMDBID: tmdbID, Type: mediaType}
	}
	return t
}

// resolve fills the names of page's items (bookmarks are stored as bare ids).
func (b *Bot) resolve(ctx context.Context, l *listing, page int, lang string) {
	start, slice := l.slice(page)
	for i := range slice {
		if slice[i].Title == "" {
			l.items[start+i] = b.card(ctx, slice[i].Type, slice[i].TMDBID, lang)
		}
	}
}

// show sends (msgID 0) or edits a listing page and remembers it by message.
func (b *Bot) show(ctx context.Context, chatID, userID, msgID int64, lang string, l *listing, page int) {
	b.resolve(ctx, l, page, lang)
	text, kb := renderList(lang, l, page, b.bookmarkSet(userID))
	if msgID == 0 {
		id, err := b.api.SendMessage(ctx, chatID, text, kb)
		if err != nil {
			b.log.Warn("telegram: send failed", "error", err)
			return
		}
		msgID = id
	} else if err := b.api.EditMessage(ctx, chatID, msgID, text, kb); err != nil {
		b.log.Debug("telegram: edit failed", "error", err)
	}
	b.remember(chatID, msgID, l)
}

func (b *Bot) search(ctx context.Context, chatID, userID int64, lang, q string) {
	b.mu.Lock()
	st := b.state(chatID)
	if time.Since(st.lastSearch) < searchMinGap {
		b.mu.Unlock()
		b.reply(ctx, chatID, tr(lang, "search.slow"), nil)
		return
	}
	st.lastSearch = time.Now()
	b.mu.Unlock()

	l := &listing{kind: "search", q: q}
	if err := b.fetchPage(ctx, l, lang, 1); err != nil {
		b.fail(ctx, chatID, lang, "search", err)
		return
	}
	b.show(ctx, chatID, userID, 0, lang, l, 1)
}

// fetchPage appends TMDB page n to l (no-op if already fetched).
func (b *Bot) fetchPage(ctx context.Context, l *listing, lang string, n int) error {
	if n <= l.tmdbPages {
		return nil
	}
	res, err := b.catalog.Search(ctx, l.q, lang, n)
	if err != nil {
		return err
	}
	l.items = append(l.items, res.Items...)
	l.tmdbPages, l.totalPages = n, res.TotalPages
	return nil
}

func (b *Bot) showContinue(ctx context.Context, chatID, userID int64, lang string) {
	tcs, err := b.sync.ListContinueWatching(userID, continueLimit)
	if err != nil {
		b.fail(ctx, chatID, lang, "continue", err)
		return
	}
	l := &listing{kind: "continue", tcs: tcs}
	for _, tc := range tcs {
		l.items = append(l.items, b.card(ctx, tc.MediaType, int(tc.TMDBID), lang))
	}
	b.show(ctx, chatID, userID, 0, lang, l, 1)
}

func (b *Bot) showBookmarks(ctx context.Context, chatID, userID int64, lang string) {
	bms, err := b.sync.ListBookmarks(userID)
	if err != nil {
		b.fail(ctx, chatID, lang, "bookmarks", err)
		return
	}
	l := &listing{kind: "bookmarks"}
	for _, bm := range bms {
		l.items = append(l.items, catalog.Title{TMDBID: int(bm.TMDBID), Type: bm.MediaType})
	}
	b.show(ctx, chatID, userID, 0, lang, l, 1)
}

// --- callbacks ------------------------------------------------------------------

func (b *Bot) handleCallback(ctx context.Context, cq *CallbackQuery) {
	chatID, msgID := cq.Message.Chat.ID, cq.Message.MessageID
	cb, err := parseCallback(cq.Data)
	if err != nil {
		_ = b.api.AnswerCallback(ctx, cq.ID, tr(defaultLang, "err.button"))
		return
	}
	userID, err := b.repo.UserByChat(chatID)
	if err != nil {
		_ = b.api.AnswerCallback(ctx, cq.ID, tr(defaultLang, "err.notlinked"))
		return
	}
	lang := b.langOf(userID)
	answer := func(text string) { _ = b.api.AnswerCallback(ctx, cq.ID, text) }
	edit := func(text string, kb *InlineKeyboardMarkup) {
		if err := b.api.EditMessage(ctx, chatID, msgID, text, kb); err != nil {
			b.log.Debug("telegram: edit failed", "error", err)
		}
	}

	switch cb.Kind {
	case "page":
		l := b.listingOf(chatID, msgID)
		if l == nil {
			answer(tr(lang, "stale"))
			return
		}
		if len(l.items) < cb.Page*pageSize && l.hasMore() {
			if err := b.fetchPage(ctx, l, lang, l.tmdbPages+1); err != nil {
				answer(tr(lang, "err.catalog"))
				return
			}
		}
		answer("")
		b.show(ctx, chatID, userID, msgID, lang, l, cb.Page)

	case "bm":
		l := b.listingOf(chatID, msgID)
		key := bmKey(cb.MediaType, cb.TMDBID)
		if (l != nil && l.kind == "bookmarks") || b.bookmarkSet(userID)[key] {
			err = b.sync.RemoveBookmark(userID, int64(cb.TMDBID), cb.MediaType)
			answer(tr(lang, "bm.removed"))
			if l != nil && l.kind == "bookmarks" {
				l.items = deleteTitle(l.items, cb.TMDBID, cb.MediaType)
			}
		} else {
			_, _, err = b.sync.AddBookmark(userID, int64(cb.TMDBID), cb.MediaType)
			answer(tr(lang, "bm.added"))
		}
		if err != nil {
			b.log.Warn("telegram: bookmark toggle failed", "error", err)
			return
		}
		if l != nil {
			b.show(ctx, chatID, userID, msgID, lang, l, cb.Page)
		}

	case "open":
		act := &action{kind: "open", tmdbID: cb.TMDBID, mediaType: cb.MediaType, resume: cb.Resume}
		if l := b.listingOf(chatID, msgID); l != nil {
			for _, t := range l.items {
				if t.TMDBID == cb.TMDBID && t.Type == cb.MediaType {
					act.title = t.Title
				}
			}
		}
		b.dispatch(ctx, cq, userID, lang, act)

	case "rc":
		if _, ok := remotePayload("", cb.Arg); !ok {
			answer(tr(lang, "err.button"))
			return
		}
		b.dispatch(ctx, cq, userID, lang, &action{kind: "remote", remote: cb.Arg})

	case "dev":
		b.mu.Lock()
		st := b.state(chatID)
		act := st.pending
		st.pending = nil
		b.mu.Unlock()
		dev := b.online(userID, cb.Arg)
		if dev == nil {
			answer(tr(lang, "open.offline"))
			return
		}
		if act == nil {
			answer(tr(lang, "stale"))
			return
		}
		b.mu.Lock()
		st.lastDev, st.lastDevAt = dev.ID, time.Now()
		b.mu.Unlock()
		b.publish(userID, *dev, act)
		answer(tr(lang, "remote.sent"))
		edit(b.doneText(lang, *dev, act), b.doneKeyboard(lang, act))

	case "home":
		home, err := b.catalog.Home(ctx, lang)
		if err != nil {
			answer(tr(lang, "err.catalog"))
			return
		}
		for _, r := range home.Rows {
			if r.ID == cb.Arg {
				answer("")
				b.show(ctx, chatID, userID, msgID, lang, &listing{kind: "home", rowTitle: r.Title, items: r.Items}, 1)
				return
			}
		}
		answer(tr(lang, "stale"))

	case "lang":
		code := normLang(cb.Arg)
		if err := b.sync.SetSetting(userID, "lang", code); err != nil {
			answer(tr(lang, "err.generic"))
			return
		}
		answer("")
		edit(tr(code, "settings.header"), settingsKeyboard(code))
		b.reply(ctx, chatID, tr(code, "lang.set"), mainMenu(code))

	case "unlink":
		switch cb.Arg {
		case "ask":
			answer("")
			edit(tr(lang, "unlink.confirm"), unlinkConfirm(lang))
		case "yes":
			if err := b.repo.UnlinkChat(chatID); err != nil {
				answer(tr(lang, "err.generic"))
				return
			}
			b.mu.Lock()
			delete(b.chats, chatID)
			b.mu.Unlock()
			answer("")
			edit(tr(lang, "unlink.done"), nil)
			b.reply(ctx, chatID, tr(lang, "unlinked"), &ReplyKeyboardRemove{RemoveKeyboard: true})
		default:
			answer("")
			edit(tr(lang, "unlink.cancel"), nil)
		}
	}
}

func deleteTitle(items []catalog.Title, tmdbID int, mediaType string) []catalog.Title {
	out := items[:0]
	for _, t := range items {
		if t.TMDBID != tmdbID || t.Type != mediaType {
			out = append(out, t)
		}
	}
	return out
}

// online returns the user's online device with id, or nil.
func (b *Bot) online(userID int64, id string) *promsync.DeviceInfo {
	for _, d := range b.hub.OnlineDevices(userID) {
		if d.ID == id {
			return &d
		}
	}
	return nil
}

// dispatch sends act to a device: the only one online, or the one picked
// recently; otherwise it asks (storing act as pending for the dev: callback).
func (b *Bot) dispatch(ctx context.Context, cq *CallbackQuery, userID int64, lang string, act *action) {
	chatID := cq.Message.Chat.ID
	devs := b.hub.OnlineDevices(userID)
	if len(devs) == 0 {
		_ = b.api.AnswerCallback(ctx, cq.ID, tr(lang, "open.none"))
		return
	}
	var target *promsync.DeviceInfo
	if len(devs) == 1 {
		target = &devs[0]
	} else {
		b.mu.Lock()
		st := b.state(chatID)
		if time.Since(st.lastDevAt) < deviceMemory {
			target = b.online(userID, st.lastDev)
		}
		if target == nil {
			st.pending = act
		}
		b.mu.Unlock()
	}
	if target == nil {
		_ = b.api.AnswerCallback(ctx, cq.ID, "")
		b.reply(ctx, chatID, tr(lang, "open.pick"), devicePicker(devs, lang))
		return
	}
	b.publish(userID, *target, act)
	if act.kind == "remote" {
		_ = b.api.AnswerCallback(ctx, cq.ID, tr(lang, "remote.sent"))
		return
	}
	_ = b.api.AnswerCallback(ctx, cq.ID, "")
	b.reply(ctx, chatID, b.doneText(lang, *target, act), b.doneKeyboard(lang, act))
}

// doneKeyboard is what follows "Opened on <TV>": the remote, so the user can
// pick a source / episode on the title page without the physical remote.
func (b *Bot) doneKeyboard(lang string, act *action) *InlineKeyboardMarkup {
	if act.kind == "remote" {
		return nil
	}
	return remoteKeyboard(lang)
}

func (b *Bot) doneText(lang string, dev promsync.DeviceInfo, act *action) string {
	if act.kind == "remote" {
		return tr(lang, "remote.sent")
	}
	return tr(lang, "open.done", html.EscapeString(deviceLabel(dev, lang)))
}

func (b *Bot) publish(userID int64, dev promsync.DeviceInfo, act *action) {
	if act.kind == "remote" {
		p, _ := remotePayload(dev.ID, act.remote)
		b.hub.Publish(userID, promsync.EventRemote, p)
		b.log.Info("telegram: remote", "user_id", userID, "action", p.Action, "device", dev.Name)
		return
	}
	b.hub.Publish(userID, promsync.EventOpenTitle, OpenTitlePayload{TMDBID: act.tmdbID, MediaType: act.mediaType, DeviceID: dev.ID, Title: act.title, Resume: act.resume})
	b.log.Info("telegram: open title", "user_id", userID, "tmdb_id", act.tmdbID, "media_type", act.mediaType, "resume", act.resume, "device", dev.Name)
}

func (b *Bot) reply(ctx context.Context, chatID int64, text string, markup any) {
	if _, err := b.api.SendMessage(ctx, chatID, text, markup); err != nil {
		b.log.Warn("telegram: send failed", "error", err)
	}
}

func (b *Bot) fail(ctx context.Context, chatID int64, lang, what string, err error) {
	b.log.Warn("telegram: "+what+" failed", "error", err)
	b.reply(ctx, chatID, tr(lang, "err.generic"), nil)
}

// Links lists the chats linked to a profile (Settings → Telegram).
func (b *Bot) Links(userID int64) ([]store.TelegramLink, error) { return b.repo.List(userID) }

// UnlinkChat removes one chat of the profile.
func (b *Bot) UnlinkChat(userID, chatID int64) error { return b.repo.UnlinkChatOfUser(userID, chatID) }
