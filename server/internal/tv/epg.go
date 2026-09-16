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

// Programme guide (docs/tv.md). Public XMLTV feeds are stored WHOLE — every
// channel of every feed we carry, for a short window — keyed by
// "<source>:<xmltv id>"; our channels point at one such key
// (tv_channels.epg_id). Matching is by name: the feed's display-names against
// our name plus iptv-org's alt_names (native spellings), normalised
// (lower-case, Cyrillic transliterated, "HD"/"канал"/… dropped). The admin
// can pin any channel to any feed channel, which is a plain UPDATE.
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
	if time.Since(time.Unix(at, 0)) > epgEvery {
		return true
	}
	// A fresh epg_at with no rows (the table was just (re)created): fetch now.
	st, err := s.repo.Stats()
	return err == nil && st.Programmes == 0
}

func (s *Service) wantCountry(c string) bool {
	for _, have := range s.countries {
		if strings.EqualFold(have, c) {
			return true
		}
	}
	return false
}

// SyncEPG re-downloads every source whose countries we serve, stores its
// channel list and programmes, then points our channels at feed channels
// (admin overrides first, name matching for the rest).
func (s *Service) SyncEPG(ctx context.Context) error {
	t0 := time.Now()
	window := timeWindow{from: t0.Add(-epgPast).Unix(), to: t0.Add(epgAhead).Unix()}
	var total, sources int
	for _, src := range epgSources {
		used := false
		for _, c := range src.Countries {
			used = used || s.wantCountry(c)
		}
		if !used {
			continue
		}
		n, err := s.grabEPG(ctx, src, window)
		if err != nil {
			s.log.Warn("tv: epg source failed", "source", src.Name, "error", err)
			continue
		}
		total += n
		sources++
		s.log.Info("tv: epg source", "source", src.Name, "programmes", n)
	}
	if sources == 0 {
		return fmt.Errorf("epg: no source succeeded")
	}
	matched, err := s.RematchEPG()
	if err != nil {
		return err
	}
	_ = s.repo.SetMeta("epg_at", fmt.Sprint(time.Now().Unix()))
	s.log.Info("tv: epg synced", "programmes", total, "channels", matched, "ms", time.Since(t0).Milliseconds())
	return nil
}

// RematchEPG recomputes epg_id for every channel from the stored feed channel
// lists: an override wins; otherwise the best name match among the sources
// of the channel's country. Returns how many channels have a guide.
func (s *Service) RematchEPG() (int, error) {
	names, err := s.repo.ChannelNames()
	if err != nil {
		return 0, err
	}
	overrides, err := s.repo.EPGOverrides()
	if err != nil {
		return 0, err
	}
	matchers := map[string]*matcher{} // source → matcher
	for _, src := range epgSources {
		list, err := s.repo.EPGChannels(src.Name)
		if err != nil {
			return 0, err
		}
		if len(list) == 0 {
			continue
		}
		m := newMatcher()
		for _, c := range list {
			m.add(c.XMLTVID, c.Names)
		}
		matchers[src.Name] = m
	}
	ids := map[string]string{}
	matched := 0
	for _, n := range names {
		key := ""
		if ov, has := overrides[n.ID]; has {
			if ov.XMLTVID != "" {
				key = ov.Source + ":" + ov.XMLTVID
			}
		} else {
			for _, src := range epgSources {
				m := matchers[src.Name]
				if m == nil || !containsFold(src.Countries, n.Country) {
					continue
				}
				if xid, ok := m.find(n); ok {
					key = src.Name + ":" + xid
					break
				}
			}
		}
		if key != "" {
			matched++
		}
		ids[n.ID] = key
	}
	return matched, s.repo.SetEPGIDs(ids)
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
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

// grabEPG streams one XMLTV feed (gzip or plain) into the store; returns the
// number of programmes kept.
func (s *Service) grabEPG(ctx context.Context, src epgSource, w timeWindow) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "promin (+https://promin.club)")
	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	var body io.Reader = resp.Body
	if strings.HasSuffix(src.URL, ".gz") || resp.Header.Get("Content-Type") == "application/x-gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return 0, err
		}
		defer gz.Close()
		body = gz
	}
	wr, err := s.repo.BeginEPG(src.Name)
	if err != nil {
		return 0, err
	}
	feed, err := parseXMLTV(body, w, func(xid string, p store.TVProgram) error {
		return wr.Add(src.Name+":"+xid, p)
	})
	if err != nil {
		wr.Rollback()
		return 0, err
	}
	if err := wr.Commit(); err != nil {
		return 0, err
	}
	for i := range feed {
		feed[i].Source = src.Name
	}
	if err := s.repo.ReplaceEPGChannels(src.Name, feed); err != nil {
		return 0, err
	}
	return wr.Count(), nil
}

// parseXMLTV streams a feed: the channel list is returned, every programme
// inside the window is handed to emit (feed channel id + programme).
func parseXMLTV(r io.Reader, w timeWindow, emit func(xid string, p store.TVProgram) error) ([]store.EPGChannel, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	var feed []store.EPGChannel
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "channel":
			var ch xmltvChannel
			if err := dec.DecodeElement(&ch, &se); err == nil && ch.ID != "" {
				var names []string
				for _, n := range ch.Names {
					if n = strings.TrimSpace(n); n != "" {
						names = append(names, n)
					}
				}
				if len(names) == 0 {
					names = []string{ch.ID}
				}
				feed = append(feed, store.EPGChannel{XMLTVID: ch.ID, Names: names})
			}
		case "programme":
			var p xmltvProgramme
			if err := dec.DecodeElement(&p, &se); err != nil || p.Channel == "" {
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
			if err := emit(p.Channel, store.TVProgram{Start: start, Stop: stop, Title: title, Desc: desc}); err != nil {
				return nil, err
			}
		}
	}
	return feed, nil
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
