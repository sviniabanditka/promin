package store

import (
	"encoding/json"
	"strings"
)

// Features is what a profile may use (docs/auth.md, admin panel). The admin
// always has everything; a profile with an empty users.features has
// everything too — restrictions are opt-in per profile.
type Features struct {
	Online      bool     `json:"online"`
	Torrents    bool     `json:"torrents"`
	YouTube     bool     `json:"youtube"`
	TV          bool     `json:"tv"`
	TVCountries []string `json:"tv_countries"` // empty = every configured country
}

func AllFeatures() Features {
	return Features{Online: true, Torrents: true, YouTube: true, TV: true, TVCountries: []string{}}
}

// ParseFeatures: "" or garbage → everything (never lock a profile out by a
// broken row).
func ParseFeatures(raw string) Features {
	if strings.TrimSpace(raw) == "" {
		return AllFeatures()
	}
	var f Features
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return AllFeatures()
	}
	if f.TVCountries == nil {
		f.TVCountries = []string{}
	}
	for i := range f.TVCountries {
		f.TVCountries[i] = strings.ToUpper(strings.TrimSpace(f.TVCountries[i]))
	}
	return f
}

func (f Features) JSON() string {
	b, _ := json.Marshal(f)
	return string(b)
}

// Has: the route-group names used by requireFeature.
func (f Features) Has(name string) bool {
	switch name {
	case "online":
		return f.Online
	case "torrents":
		return f.Torrents
	case "youtube":
		return f.YouTube
	case "tv":
		return f.TV
	}
	return false
}

func (f Features) AllowsCountry(code string) bool {
	if len(f.TVCountries) == 0 {
		return true
	}
	for _, c := range f.TVCountries {
		if strings.EqualFold(c, code) {
			return true
		}
	}
	return false
}

// Features of the user: admin → all.
func (u User) Features() Features {
	if u.IsAdmin() {
		return AllFeatures()
	}
	return ParseFeatures(u.FeaturesRaw)
}
