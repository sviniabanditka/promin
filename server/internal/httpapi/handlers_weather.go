package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/sviniabanditka/promin/server/internal/weather"
)

// weatherHandlers serves the screensaver's forecast strip.
type weatherHandlers struct {
	svc *weather.Service
	// place is the configured city (PROMIN_WEATHER_PLACE). When set it wins over
	// IP guessing: a household TV does not move, so an explicit city is both
	// exact and free of any geolocation dependency.
	place string
}

// countryCapitals maps the one geo signal Cloudflare gives us for free
// (CF-IPCountry) to a searchable place, so the strip works with zero config.
// Country-level is approximate by nature — set PROMIN_WEATHER_PLACE for exact.
var countryCapitals = map[string]string{
	"UA": "Kyiv", "PL": "Warsaw", "DE": "Berlin", "CZ": "Prague", "SK": "Bratislava",
	"RO": "Bucharest", "MD": "Chisinau", "HU": "Budapest", "LT": "Vilnius",
	"LV": "Riga", "EE": "Tallinn", "GB": "London", "US": "New York", "CA": "Toronto",
	"ES": "Madrid", "PT": "Lisbon", "IT": "Rome", "FR": "Paris", "NL": "Amsterdam",
	"BE": "Brussels", "AT": "Vienna", "CH": "Zurich", "SE": "Stockholm",
	"NO": "Oslo", "FI": "Helsinki", "DK": "Copenhagen", "IE": "Dublin",
	"TR": "Istanbul", "GE": "Tbilisi", "AM": "Yerevan", "KZ": "Almaty",
	"IL": "Tel Aviv", "AE": "Dubai", "BG": "Sofia", "GR": "Athens", "HR": "Zagreb",
	"RS": "Belgrade", "SI": "Ljubljana",
}

// resolvePlace picks the location, most trustworthy source first:
//  1. Cloudflare's precise visitor headers, IF the operator enabled the
//     "visitor location" managed transform (free, but off by default).
//  2. The configured city — an explicit override always wins over guessing.
//  3. GeoIP on the visitor's address — city-level, automatic, no setup.
//  4. CF-IPCountry → that country's main city: last resort when GeoIP is
//     unavailable (rate limit, both providers down, private address).
func (h *weatherHandlers) resolvePlace(r *http.Request, lang string) (weather.Place, bool) {
	latS, lonS := r.Header.Get("CF-IPLatitude"), r.Header.Get("CF-IPLongitude")
	if latS != "" && lonS != "" {
		lat, e1 := strconv.ParseFloat(latS, 64)
		lon, e2 := strconv.ParseFloat(lonS, 64)
		if e1 == nil && e2 == nil && (lat != 0 || lon != 0) {
			name := r.Header.Get("CF-IPCity")
			if name == "" {
				name = "—"
			}
			return weather.Place{Name: name, Lat: lat, Lon: lon}, true
		}
	}

	if h.place != "" {
		if p, err := h.svc.Geocode(r.Context(), h.place, lang); err == nil {
			return p, true
		}
	}

	// City-level GeoIP on the visitor's own address.
	if p, err := h.svc.GeoIP(r.Context(), clientIP(r)); err == nil && p.Lat != 0 {
		return p, true
	}

	if cc := strings.ToUpper(r.Header.Get("CF-IPCountry")); cc != "" {
		if city, ok := countryCapitals[cc]; ok {
			if p, err := h.svc.Geocode(r.Context(), city, lang); err == nil {
				return p, true
			}
		}
	}
	return weather.Place{}, false
}

// weather serves the forecast. Never a hard error: the screensaver simply omits
// the strip if this fails, so a weather outage must not look like a bug.
func (h *weatherHandlers) forecast(w http.ResponseWriter, r *http.Request) {
	lang := r.URL.Query().Get("lang")
	if lang == "" {
		lang = "uk"
	}
	place, ok := h.resolvePlace(r, lang)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	f, err := h.svc.Forecast(r.Context(), place, lang)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "forecast": f})
}
