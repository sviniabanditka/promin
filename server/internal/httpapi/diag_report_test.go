package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatReportReadsLikeAMessage(t *testing.T) {
	data := json.RawMessage(`{"title":"Dune","episode":"S1E3","source":"filmix","kind":"decode","media_error":"MEDIA_ERR_DECODE","url":"/relay?u=abc","platform":"tizen","hevc":false,"demuxed_hls":false,"engine":"hlsjs"}`)
	got := formatReport("kostya", "1.2.3.4", "Mozilla/5.0 (SMART-TV; Linux; Tizen 4.0)", data)
	for _, want := range []string{"Не грає у kostya", "Dune · S1E3", "Джерело: filmix", "Помилка: decode (MEDIA_ERR_DECODE)", "URL: /relay?u=abc", "Панель: tizen, hevc=false, demuxed_hls=false, engine=hlsjs", "UA: Mozilla", "IP: 1.2.3.4"} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	// Garbage data still yields a message, not a panic.
	if got := formatReport("", "", "", json.RawMessage(`not json`)); !strings.HasPrefix(got, "🛠 Не грає") {
		t.Fatalf("bad data: %q", got)
	}
}
