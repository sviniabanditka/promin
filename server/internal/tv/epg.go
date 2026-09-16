package tv

import (
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/sviniabanditka/promin/server/internal/store"
)

// Programme guide. There is no XMLTV feed keyed by iptv-org ids, so we take
// public country feeds and match channels by name: the feed's display-names
// against our name plus iptv-org's alt_names (native spellings), normalised
// (lower-case, Cyrillic transliterated, "HD"/"канал"/… dropped). Unmatched
// channels simply have no guide.
type epgSource struct {
	Name      string
	URL       string
	Countries []string
}

var epgSources = []epgSource{
	{Name: "iptvx", URL: "https://iptvx.one/epg/epg.xml.gz", Countries: []string{"UA", "RU"}},
	{Name: "epgshare-uk", URL: "https://epgshare01.online/epgshare01/epg_ripper_UK1.xml.gz", Countries: []string{"UK"}},
	{Name: "epgshare-us", URL: "https://epgshare01.online/epgshare01/epg_ripper_US2.xml.gz", Countries: []string{"US"}},
}

const (
	epgEvery = 12 * time.Hour
	epgPast  = 12 * time.Hour
	epgAhead = 3 * 24 * time.Hour
	epgDesc  = 600 // runes kept of a description
)

func (s *Service) epgStale() bool {
	var at int64
	fmt.Sscan(s.repo.Meta("epg_at"), &at)
	return time.Since(time.Unix(at, 0)) > epgEvery
}

// SyncEPG rebuilds tv_programs from every source whose countries we serve.
func (s *Service) SyncEPG(ctx context.Context) error {
	names, err := s.repo.ChannelNames()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, c := range s.countries {
		want[strings.ToUpper(c)] = true
	}
	t0 := time.Now()
	window := timeWindow{from: t0.Add(-epgPast).Unix(), to: t0.Add(epgAhead).Unix()}
	overrides, err := s.repo.EPGOverrides()
	if err != nil {
		return err
	}
	var all []store.TVProgram
	epgIDs := map[string]string{}
	for _, src := range epgSources {
		var mine []store.TVChannelName
		for _, n := range names {
			for _, c := range src.Countries {
				if n.Country == c && want[c] {
					mine = append(mine, n)
				}
			}
		}
		if len(mine) == 0 {
			continue
		}
		progs, matched, feed, err := s.grabEPG(ctx, src, mine, window, overrides)
		if err != nil {
			s.log.Warn("tv: epg source failed", "source", src.Name, "error", err)
			continue
		}
		if err := s.repo.ReplaceEPGChannels(src.Name, feed); err != nil {
			s.log.Warn("tv: epg feed channels", "source", src.Name, "error", err)
		}
		for our, xid := range matched {
			epgIDs[our] = src.Name + ":" + xid
		}
		all = append(all, progs...)
		s.log.Info("tv: epg source", "source", src.Name, "channels", len(mine), "matched", len(matched), "programmes", len(progs))
	}
	if len(all) == 0 {
		return fmt.Errorf("epg: nothing matched")
	}
	if err := s.repo.ReplaceEPG(all, epgIDs); err != nil {
		return err
	}
	_ = s.repo.SetMeta("epg_at", fmt.Sprint(time.Now().Unix()))
	s.log.Info("tv: epg synced", "programmes", len(all), "channels", len(epgIDs), "ms", time.Since(t0).Milliseconds())
	return nil
}

type timeWindow struct{ from, to int64 }

type xmltvChannel struct {
	ID    string   `xml:"id,attr"`
	Names []string `xml:"display-name"`
}

type xmltvProgramme struct {
	Start   string   `xml:"start,attr"`
	Stop    string   `xml:"stop,attr"`
	Channel string   `xml:"channel,attr"`
	Titles  []string `xml:"title"`
	Desc    string   `xml:"desc"`
}

// grabEPG streams one XMLTV feed (gzip or plain) and returns the programmes
// of the channels it could match, plus our-id → xmltv-id for those.
func (s *Service) grabEPG(ctx context.Context, src epgSource, ours []store.TVChannelName, w timeWindow, overrides map[string]store.EPGOverride) ([]store.TVProgram, map[string]string, []store.EPGChannel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	req.Header.Set("User-Agent", "promin (+https://promin.club)")
	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var body io.Reader = resp.Body
	if strings.HasSuffix(src.URL, ".gz") || resp.Header.Get("Content-Type") == "application/x-gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, nil, nil, err
		}
		defer gz.Close()
		body = gz
	}
	return parseXMLTV(body, ours, w, src.Name, overrides)
}

// parseXMLTV: <channel> elements come first in XMLTV, so the matcher is
// complete by the first <programme>. An admin override (docs/tv.md) pins a
// channel to a feed id of one source — or to no guide — and skips matching.
func parseXMLTV(r io.Reader, ours []store.TVChannelName, w timeWindow, srcName string, overrides map[string]store.EPGOverride) ([]store.TVProgram, map[string]string, []store.EPGChannel, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	m := newMatcher()
	var xmlToOurs map[string][]string // xmltv id → our ids
	matched := map[string]string{}
	var out []store.TVProgram
	var feed []store.EPGChannel
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "channel":
			var ch xmltvChannel
			if err := dec.DecodeElement(&ch, &se); err == nil {
				m.add(ch.ID, ch.Names)
				feed = append(feed, store.EPGChannel{Source: srcName, XMLTVID: ch.ID, Names: ch.Names})
			}
		case "programme":
			if xmlToOurs == nil {
				xmlToOurs = map[string][]string{}
				for _, o := range ours {
					if ov, has := overrides[o.ID]; has {
						if ov.Source == srcName && ov.XMLTVID != "" {
							matched[o.ID] = ov.XMLTVID
							xmlToOurs[ov.XMLTVID] = append(xmlToOurs[ov.XMLTVID], o.ID)
						}
						continue // pinned elsewhere or to "no guide"
					}
					if xid, ok := m.find(o); ok {
						matched[o.ID] = xid
						xmlToOurs[xid] = append(xmlToOurs[xid], o.ID)
					}
				}
			}
			var p xmltvProgramme
			if err := dec.DecodeElement(&p, &se); err != nil {
				continue
			}
			ids := xmlToOurs[p.Channel]
			if len(ids) == 0 {
				continue
			}
			start, stop := parseXMLTVTime(p.Start), parseXMLTVTime(p.Stop)
			if start == 0 || stop <= start || stop < w.from || start > w.to {
				continue
			}
			title := ""
			if len(p.Titles) > 0 {
				title = strings.TrimSpace(p.Titles[0])
			}
			if title == "" {
				continue
			}
			desc := strings.TrimSpace(p.Desc)
			if rs := []rune(desc); len(rs) > epgDesc {
				desc = string(rs[:epgDesc-1]) + "…"
			}
			for _, id := range ids {
				out = append(out, store.TVProgram{ChannelID: id, Start: start, Stop: stop, Title: title, Desc: desc})
			}
		}
	}
	return out, matched, feed, nil
}

func parseXMLTVTime(s string) int64 {
	s = strings.TrimSpace(s)
	if t, err := time.Parse("20060102150405 -0700", s); err == nil {
		return t.Unix()
	}
	if t, err := time.Parse("20060102150405", s); err == nil {
		return t.Unix()
	}
	return 0
}

// ---- name matching ------------------------------------------------------------

type matcher struct {
	strict map[string]string // normalised display-name → xmltv id
	loose  map[string]string // …with filler words dropped
}

func newMatcher() *matcher {
	return &matcher{strict: map[string]string{}, loose: map[string]string{}}
}

func (m *matcher) add(id string, names []string) {
	for _, n := range names {
		if k := normStrict(n); k != "" {
			if _, dup := m.strict[k]; !dup {
				m.strict[k] = id
			}
		}
		if k := normLoose(n); k != "" {
			if _, dup := m.loose[k]; !dup {
				m.loose[k] = id
			}
		}
	}
}

// find tries every spelling we know strictly first, then loosely, so
// "1+1 Ukraina" prefers the feed's "1+1 Україна" over a bare "1+1".
func (m *matcher) find(c store.TVChannelName) (string, bool) {
	names := append([]string{c.Name}, c.AltNames...)
	for _, n := range names {
		if id, ok := m.strict[normStrict(n)]; ok {
			return id, true
		}
	}
	for _, n := range names {
		if k := normLoose(n); k != "" {
			if id, ok := m.loose[k]; ok {
				return id, true
			}
		}
	}
	return "", false
}

var cyr = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'ґ': "g", 'д': "d", 'е': "e", 'є': "e", 'ё': "e", 'ж': "zh", 'з': "z",
	'и': "i", 'і': "i", 'ї': "i", 'й': "i", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r",
	'с': "s", 'т': "t", 'у': "u", 'ф': "f", 'х': "h", 'ц': "c", 'ч': "ch", 'ш': "sh", 'щ': "sch", 'ъ': "", 'ы': "y",
	'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
}

var (
	fillerRe = regexp.MustCompile(`\b(hd|uhd|fhd|4k|tv|kanal|channel|telekanal|ua|ukraine|ukraina|international|russia|rossiya)\b`)
	junkRe   = regexp.MustCompile(`[^a-z0-9+]+`)
)

func translit(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(s) {
		if t, ok := cyr[r]; ok {
			sb.WriteString(t)
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func normStrict(s string) string {
	return junkRe.ReplaceAllString(translit(s), "")
}

func normLoose(s string) string {
	return junkRe.ReplaceAllString(fillerRe.ReplaceAllString(translit(s), " "), "")
}
