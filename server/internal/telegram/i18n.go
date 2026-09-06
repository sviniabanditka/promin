package telegram

import "fmt"

// defaultLang is used when a profile has no "lang" setting.
const defaultLang = "uk"

// langs lists supported languages in menu order.
var langs = []string{"uk", "ru", "en"}

// texts holds every user-visible string; tr() looks them up. Keys are
// identical across languages (TestI18nParity).
var texts = map[string]map[string]string{
	"uk": {
		"menu.search":    "🔍 Пошук",
		"menu.continue":  "▶ Продовжити",
		"menu.bookmarks": "★ Закладки",
		"menu.watch":     "🔥 Що подивитись",
		"menu.remote":    "🎛 Пульт",
		"menu.settings":  "⚙ Налаштування",

		"cmd.start":  "Почати / прив'язати чат",
		"cmd.help":   "Підказка",
		"cmd.menu":   "Показати меню",
		"cmd.unlink": "Відв'язати чат",

		"hello":           "Привіт! 👋",
		"help":            "Надішли назву фільму або серіалу — я знайду і відкрию на телевізорі. Кнопки внизу: продовжити перегляд, закладки, добірки, пульт і налаштування.",
		"unlinked":        "Цей чат не прив'язано. Відкрий Promin → Налаштування → Telegram і надішли мені код.",
		"link.ok":         "✅ Готово, чат прив'язано до твого профілю Promin.",
		"link.bad":        "Код невірний або застарів. Отримай новий у Promin → Налаштування → Telegram.",
		"unlink.confirm":  "Відв'язати цей чат від Promin?",
		"unlink.yes":      "Так, відв'язати",
		"unlink.no":       "Скасувати",
		"unlink.done":     "Чат відв'язано від Promin.",
		"unlink.cancel":   "Залишаємо як є.",
		"search.prompt":   "Напиши назву фільму або серіалу 🔎",
		"search.header":   "🔎 «%s» — сторінка %d",
		"search.empty":    "Нічого не знайдено.",
		"search.slow":     "Не так швидко — зачекай секунду.",
		"stale":           "Це повідомлення застаріло — натисни кнопку меню ще раз.",
		"open.btn":        "Відкрити на ТВ",
		"open.done":       "Відкрито на %s",
		"open.none":       "Немає пристроїв онлайн — відкрий Promin на телевізорі",
		"open.pick":       "На якому пристрої?",
		"open.offline":    "Пристрій уже офлайн",
		"device":          "пристрій %s",
		"continue.header": "▶ Продовжити перегляд",
		"continue.empty":  "Немає незавершених переглядів.",
		"bm.header":       "★ Закладки — сторінка %d",
		"bm.empty":        "Закладок поки немає.",
		"bm.remove":       "✕ Видалити",
		"bm.added":        "Додано в закладки",
		"bm.removed":      "Видалено із закладок",
		"watch.header":    "🔥 Що подивитись?",
		"watch.page":      "%s — сторінка %d",
		"watch.empty":     "Добірка порожня.",
		"remote.header":   "🎛 Пульт",
		"remote.sent":     "Надіслано",
		"remote.night":    "🌙 Ніч",
		"remote.sleep":    "😴 Сон 30 хв",
		"settings.header": "⚙ Налаштування\nМова інтерфейсу (спільна з ТВ):",
		"settings.unlink": "Відв'язати чат",
		"lang.set":        "Мову змінено 🇺🇦",
		"err.generic":     "Щось пішло не так, спробуй ще раз пізніше.",
		"err.catalog":     "Каталог недоступний, спробуй пізніше",
		"err.button":      "Незрозуміла кнопка",
		"err.notlinked":   "Спочатку прив'яжи чат до Promin",
	},
	"ru": {
		"menu.search":    "🔍 Поиск",
		"menu.continue":  "▶ Продолжить",
		"menu.bookmarks": "★ Закладки",
		"menu.watch":     "🔥 Что посмотреть",
		"menu.remote":    "🎛 Пульт",
		"menu.settings":  "⚙ Настройки",

		"cmd.start":  "Начать / привязать чат",
		"cmd.help":   "Подсказка",
		"cmd.menu":   "Показать меню",
		"cmd.unlink": "Отвязать чат",

		"hello":           "Привет! 👋",
		"help":            "Отправь название фильма или сериала — я найду и открою на телевизоре. Кнопки внизу: продолжить просмотр, закладки, подборки, пульт и настройки.",
		"unlinked":        "Этот чат не привязан. Открой Promin → Настройки → Telegram и пришли мне код.",
		"link.ok":         "✅ Готово, чат привязан к твоему профилю Promin.",
		"link.bad":        "Код неверный или устарел. Получи новый в Promin → Настройки → Telegram.",
		"unlink.confirm":  "Отвязать этот чат от Promin?",
		"unlink.yes":      "Да, отвязать",
		"unlink.no":       "Отмена",
		"unlink.done":     "Чат отвязан от Promin.",
		"unlink.cancel":   "Оставляем как есть.",
		"search.prompt":   "Напиши название фильма или сериала 🔎",
		"search.header":   "🔎 «%s» — страница %d",
		"search.empty":    "Ничего не найдено.",
		"search.slow":     "Не так быстро — подожди секунду.",
		"stale":           "Это сообщение устарело — нажми кнопку меню ещё раз.",
		"open.btn":        "Открыть на ТВ",
		"open.done":       "Открыто на %s",
		"open.none":       "Нет устройств онлайн — открой Promin на телевизоре",
		"open.pick":       "На каком устройстве?",
		"open.offline":    "Устройство уже офлайн",
		"device":          "устройство %s",
		"continue.header": "▶ Продолжить просмотр",
		"continue.empty":  "Нет незавершённых просмотров.",
		"bm.header":       "★ Закладки — страница %d",
		"bm.empty":        "Закладок пока нет.",
		"bm.remove":       "✕ Удалить",
		"bm.added":        "Добавлено в закладки",
		"bm.removed":      "Удалено из закладок",
		"watch.header":    "🔥 Что посмотреть?",
		"watch.page":      "%s — страница %d",
		"watch.empty":     "Подборка пуста.",
		"remote.header":   "🎛 Пульт",
		"remote.sent":     "Отправлено",
		"remote.night":    "🌙 Ночь",
		"remote.sleep":    "😴 Сон 30 мин",
		"settings.header": "⚙ Настройки\nЯзык интерфейса (общий с ТВ):",
		"settings.unlink": "Отвязать чат",
		"lang.set":        "Язык изменён 🇷🇺",
		"err.generic":     "Что-то пошло не так, попробуй позже.",
		"err.catalog":     "Каталог недоступен, попробуй позже",
		"err.button":      "Непонятная кнопка",
		"err.notlinked":   "Сначала привяжи чат к Promin",
	},
	"en": {
		"menu.search":    "🔍 Search",
		"menu.continue":  "▶ Continue",
		"menu.bookmarks": "★ Bookmarks",
		"menu.watch":     "🔥 What to watch",
		"menu.remote":    "🎛 Remote",
		"menu.settings":  "⚙ Settings",

		"cmd.start":  "Start / link this chat",
		"cmd.help":   "Help",
		"cmd.menu":   "Show the menu",
		"cmd.unlink": "Unlink this chat",

		"hello":           "Hi! 👋",
		"help":            "Send me a movie or series title — I'll find it and open it on the TV. Buttons below: continue watching, bookmarks, picks, remote and settings.",
		"unlinked":        "This chat isn't linked. Open Promin → Settings → Telegram and send me the code.",
		"link.ok":         "✅ Done, this chat is linked to your Promin profile.",
		"link.bad":        "The code is wrong or expired. Get a new one in Promin → Settings → Telegram.",
		"unlink.confirm":  "Unlink this chat from Promin?",
		"unlink.yes":      "Yes, unlink",
		"unlink.no":       "Cancel",
		"unlink.done":     "Chat unlinked from Promin.",
		"unlink.cancel":   "Kept as is.",
		"search.prompt":   "Type a movie or series title 🔎",
		"search.header":   "🔎 “%s” — page %d",
		"search.empty":    "Nothing found.",
		"search.slow":     "Not so fast — wait a second.",
		"stale":           "This message is out of date — press the menu button again.",
		"open.btn":        "Open on TV",
		"open.done":       "Opened on %s",
		"open.none":       "No devices online — open Promin on the TV",
		"open.pick":       "Which device?",
		"open.offline":    "Device is already offline",
		"device":          "device %s",
		"continue.header": "▶ Continue watching",
		"continue.empty":  "Nothing to continue.",
		"bm.header":       "★ Bookmarks — page %d",
		"bm.empty":        "No bookmarks yet.",
		"bm.remove":       "✕ Remove",
		"bm.added":        "Added to bookmarks",
		"bm.removed":      "Removed from bookmarks",
		"watch.header":    "🔥 What to watch?",
		"watch.page":      "%s — page %d",
		"watch.empty":     "This list is empty.",
		"remote.header":   "🎛 Remote",
		"remote.sent":     "Sent",
		"remote.night":    "🌙 Night",
		"remote.sleep":    "😴 Sleep 30 min",
		"settings.header": "⚙ Settings\nInterface language (shared with the TV):",
		"settings.unlink": "Unlink chat",
		"lang.set":        "Language changed 🇬🇧",
		"err.generic":     "Something went wrong, try again later.",
		"err.catalog":     "Catalog unavailable, try later",
		"err.button":      "Unknown button",
		"err.notlinked":   "Link this chat to Promin first",
	},
}

// langNames are the settings-menu labels (not translated: each in its own language).
var langNames = map[string]string{"uk": "🇺🇦 Українська", "ru": "🇷🇺 Русский", "en": "🇬🇧 English"}

// tr returns key in lang (uk fallback), formatted with args when given.
func tr(lang, key string, args ...any) string {
	s, ok := texts[lang][key]
	if !ok {
		s = texts[defaultLang][key]
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// normLang maps a profile/Telegram language code to a supported one.
func normLang(code string) string {
	if len(code) >= 2 {
		code = code[:2]
	}
	if _, ok := texts[code]; ok {
		return code
	}
	return defaultLang
}

// menuActions are the main-menu keys in display order (two per row).
var menuActions = []string{"search", "continue", "bookmarks", "watch", "remote", "settings"}

// mainMenu is the persistent reply keyboard in lang.
func mainMenu(lang string) *ReplyKeyboardMarkup {
	kb := &ReplyKeyboardMarkup{ResizeKeyboard: true}
	for i := 0; i < len(menuActions); i += 2 {
		kb.Keyboard = append(kb.Keyboard, []KeyboardButton{
			{Text: tr(lang, "menu."+menuActions[i])},
			{Text: tr(lang, "menu."+menuActions[i+1])},
		})
	}
	return kb
}

// menuAction maps a pressed main-menu label (in any language) to its
// action key; "" for ordinary text.
func menuAction(text string) string {
	for _, l := range langs {
		for _, a := range menuActions {
			if texts[l]["menu."+a] == text {
				return a
			}
		}
	}
	return ""
}

// botCommands is the setMyCommands list in lang.
func botCommands(lang string) []BotCommand {
	return []BotCommand{
		{Command: "start", Description: tr(lang, "cmd.start")},
		{Command: "help", Description: tr(lang, "cmd.help")},
		{Command: "menu", Description: tr(lang, "cmd.menu")},
		{Command: "unlink", Description: tr(lang, "cmd.unlink")},
	}
}
