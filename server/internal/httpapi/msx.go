package httpapi

import (
	"fmt"
	"net/http"
	"strings"
)

// msxStartHandler serves /msx/start.json dynamically, deriving the base URL
// from the request Host + scheme instead of hardcoding it or using MSX's
// {domain} placeholder (which some MSX builds don't resolve — it leaked the
// literal "domain", causing "Data error: code 0").
//
// Every TV gets the host it asked for (promin.club). Which host an old
// Samsung should actually live on is decided by the device-local "Режим
// старого ТВ" switch in the app (core/legacy.ts steers to PROMIN_H1_HOST) —
// deliberately no User-Agent sniffing here (docs/streaming.md).
func msxStartHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme := "https"
		if r.TLS == nil && !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "http"
		}
		base := scheme + "://" + r.Host

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Cache-Control", "no-cache")
		fmt.Fprintf(w, `{
	"name": "Promin",
	"version": "latest",
	"parameter": "content:%s/msx/start.json",
	"action": "link:%s"
}
`, base, base)
	})
}
