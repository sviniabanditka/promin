package httpapi

import (
	"regexp"
	"strings"
	"testing"
)

// The onboarding page translates itself client-side: every data-t="key" in the
// markup must have an entry in the T dictionary, every entry must be a
// [uk, ru, en] triple, and no entry may be dead.
func TestOnboardingI18n(t *testing.T) {
	keys := map[string]bool{}
	for _, m := range regexp.MustCompile(`data-t="([a-z0-9_]+)"`).FindAllStringSubmatch(onboardingHTML, -1) {
		keys[m[1]] = true
	}
	keys["title"], keys["copied"] = true, true // set from JS, not markup

	start := strings.Index(onboardingHTML, "var T = {")
	end := strings.Index(onboardingHTML[start:], "\n  };")
	if start < 0 || end < 0 {
		t.Fatal("T dictionary not found")
	}
	dict := map[string]bool{}
	entry := regexp.MustCompile(`(?m)^\s+([a-z0-9_]+): \[(".*?"), (".*?"), (".*?")\]`)
	for _, line := range strings.Split(onboardingHTML[start:start+end], "\n")[1:] {
		m := entry.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("dictionary line is not a [uk, ru, en] triple: %q", strings.TrimSpace(line))
			continue
		}
		dict[m[1]] = true
		for i, s := range m[2:] {
			if s == `""` {
				t.Errorf("%s: empty translation #%d", m[1], i)
			}
		}
	}
	for k := range keys {
		if !dict[k] {
			t.Errorf("data-t=%q has no dictionary entry", k)
		}
	}
	for k := range dict {
		if !keys[k] {
			t.Errorf("dictionary entry %q is unused", k)
		}
	}
	if len(dict) < 100 {
		t.Errorf("dictionary suspiciously small: %d", len(dict))
	}
}
