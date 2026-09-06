package catalog

// tmdbLanguage maps the client-facing lang query param (uk|ru|en) to the
// TMDB language tag. Unknown/empty values default to Ukrainian, per
// docs/api.md ("lang (uk|ru|en, дефолт uk)").
func tmdbLanguage(lang string) string {
	switch lang {
	case "ru":
		return "ru-RU"
	case "en":
		return "en-US"
	case "uk", "":
		return "uk-UA"
	default:
		return "uk-UA"
	}
}

// normalizeLang canonicalizes the lang query param itself (used as part of
// tmdb_cache keys, so it must be stable regardless of client input).
func normalizeLang(lang string) string {
	switch lang {
	case "ru", "en", "uk":
		return lang
	default:
		return "uk"
	}
}
