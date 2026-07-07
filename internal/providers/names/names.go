// Package names normalizes and matches game titles across catalogs:
// casefolding, trademark glyphs, punctuation, and edition suffixes.
// Matching is exact on normalized keys — deterministic and
// offline-testable; no fuzzy distance.
package names

import "strings"

// Suffixes are compared against already-normalized text, so they must be
// written in normalized form themselves (e.g. "directors cut").
var editionSuffixes = []string{
	"game of the year edition",
	"goty edition",
	"goty",
	"digital deluxe edition",
	"deluxe edition",
	"definitive edition",
	"ultimate edition",
	"complete edition",
	"enhanced edition",
	"special edition",
	"anniversary edition",
	"standard edition",
	"directors cut",
}

// Normalize reduces a title to a comparison key: lowercase, ASCII
// alphanumerics kept, separators collapsed to single spaces, every other
// rune dropped, one trailing edition suffix removed.
func Normalize(title string) string {
	lower := strings.ToLower(title)
	var b strings.Builder
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == ':', r == '_', r == '/':
			b.WriteRune(' ')
			// everything else (™®©, apostrophes, accents, dashes beyond
			// ASCII) is dropped
		}
	}
	s := strings.Join(strings.Fields(b.String()), " ")
	for _, suffix := range editionSuffixes {
		if trimmed, ok := strings.CutSuffix(s, " "+suffix); ok {
			s = trimmed
			break
		}
	}
	return s
}

// Entry is one catalog title with its optional store URL.
type Entry struct {
	Name string
	URL  *string
}

// Set is a normalized-name lookup built from catalog entries.
type Set struct {
	m map[string]Entry
}

func NewSet() *Set { return &Set{m: make(map[string]Entry)} }

// Add registers a catalog entry under its normalized key. First entry
// wins on collisions; empty keys are ignored.
func (s *Set) Add(name string, url *string) {
	key := Normalize(name)
	if key == "" {
		return
	}
	if _, exists := s.m[key]; !exists {
		s.m[key] = Entry{Name: name, URL: url}
	}
}

// Lookup tries each candidate name in order (canonical title first, then
// alternatives) and returns the first entry whose normalized key matches.
func (s *Set) Lookup(candidates ...string) (Entry, bool) {
	for _, c := range candidates {
		if e, ok := s.m[Normalize(c)]; ok {
			return e, true
		}
	}
	return Entry{}, false
}
