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

func TestParseXMLTVStreamsWindowedProgrammes(t *testing.T) {
	w := timeWindow{from: 1789000000, to: 1790000000} // 2026-09
	var got []string
	feed, err := parseXMLTV(strings.NewReader(xmltvFixture), w, func(xid string, p store.TVProgram) error {
		got = append(got, xid+"/"+p.Title)
		if xid == "1-1-ukraina" && (p.Desc != "Новини" || p.Start != 1789542000) {
			t.Fatalf("bad programme: %+v", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(feed) != 3 || feed[0].Names[0] != "1+1 Україна" {
		t.Fatalf("feed: %+v", feed)
	}
	if strings.Join(got, ",") != "1-1-ukraina/ТСН,24kanal/Ранок" { // 2020 is outside the window
		t.Fatalf("programmes: %v", got)
	}
}

func TestMatcherPrefersStrictThenLoose(t *testing.T) {
	m := newMatcher()
	m.add("1-1-ukraina", []string{"1+1 Україна", "1+1 Ukraine HD"})
	m.add("1-1", []string{"1+1"})
	m.add("24kanal", []string{"24 Канал"})
	cases := map[string]store.TVChannelName{
		"1-1-ukraina": {ID: "1Plus1Ukraina.ua", Name: "1+1 Ukraina", AltNames: []string{"1+1 Україна"}},
		"1-1":         {ID: "1Plus1.ua", Name: "1+1"},
		"24kanal":     {ID: "24Kanal.ua", Name: "24 Kanal", AltNames: []string{"24 Канал"}},
	}
	for want, c := range cases {
		if got, ok := m.find(c); !ok || got != want {
			t.Fatalf("%s → %q (%v), want %s", c.Name, got, ok, want)
		}
	}
	if _, ok := m.find(store.TVChannelName{Name: "Nope TV"}); ok {
		t.Fatal("Nope must not match")
	}
	if normLoose("Discovery Channel HD") != normLoose("Discovery") {
		t.Fatal("filler words must not matter")
	}
}
