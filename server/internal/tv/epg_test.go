package tv

import (
	"strings"
	"testing"

	"github.com/sviniabanditka/promin/server/internal/store"
)

const xmltvFixture = `<?xml version="1.0" encoding="UTF-8"?>
<tv>
  <channel id="1-1-ukraina"><display-name>1+1 Україна</display-name><display-name>1+1 Ukraine HD</display-name></channel>
  <channel id="1-1"><display-name>1+1</display-name></channel>
  <channel id="24kanal"><display-name>24 Канал</display-name></channel>
  <programme start="20260916100000 +0300" stop="20260916110000 +0300" channel="1-1-ukraina"><title lang="uk">ТСН</title><desc>Новини</desc></programme>
  <programme start="20260916100000 +0300" stop="20260916120000 +0300" channel="24kanal"><title>Ранок</title></programme>
  <programme start="20200101000000 +0300" stop="20200101010000 +0300" channel="24kanal"><title>Old</title></programme>
</tv>`

func TestParseXMLTVMatchesByNameAndAltName(t *testing.T) {
	ours := []store.TVChannelName{
		{ID: "1Plus1Ukraina.ua", Name: "1+1 Ukraina", AltNames: []string{"1+1 Україна"}},
		{ID: "1Plus1.ua", Name: "1+1"},
		{ID: "24Kanal.ua", Name: "24 Kanal", AltNames: []string{"24 Канал"}},
		{ID: "Nope.ua", Name: "Nope TV"},
	}
	w := timeWindow{from: 1789000000, to: 1790000000} // 2026-09
	progs, matched, feed, err := parseXMLTV(strings.NewReader(xmltvFixture), ours, w, "test", map[string]store.EPGOverride{
		"Nope.ua": {ChannelID: "Nope.ua", Source: "test", XMLTVID: "24kanal"}, // pinned by the admin
		"1Plus1.ua": {ChannelID: "1Plus1.ua", Source: "test", XMLTVID: ""},        // pinned to no guide
	})
	if err != nil {
		t.Fatal(err)
	}
	if matched["1Plus1Ukraina.ua"] != "1-1-ukraina" || matched["24Kanal.ua"] != "24kanal" || matched["Nope.ua"] != "24kanal" {
		t.Fatalf("bad match: %v", matched)
	}
	if _, ok := matched["1Plus1.ua"]; ok {
		t.Fatal("1+1 is pinned to no guide")
	}
	if len(feed) != 3 {
		t.Fatalf("feed channels: %d", len(feed))
	}
	if len(progs) != 3 { // ТСН for 1+1 Ukraina, Ранок for 24 Kanal and for Nope (pinned); 2020 is outside the window
		t.Fatalf("want 3 programmes, got %d: %+v", len(progs), progs)
	}
	if progs[0].Title != "ТСН" || progs[0].Desc != "Новини" || progs[0].Start != 1789542000 {
		t.Fatalf("bad programme: %+v", progs[0])
	}
}

func TestNormLoose(t *testing.T) {
	if normLoose("Discovery Channel HD") != normLoose("Discovery") {
		t.Fatal("filler words must not matter")
	}
	if normStrict("1+1 International") == normStrict("1+1") {
		t.Fatal("strict must keep the distinction")
	}
}
