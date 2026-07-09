package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/varigg/mediatracker/internal/store"
)

// Nav is the layout-level view model: active tab and per-group counts.
type Nav struct {
	Active string // "" (home) | "movies-tv" | "books" | "games"
	Counts map[string]int
}

// groupTypes maps a URL group to the media types it contains. The
// movies-tv group is the only multi-type group (spec Section 4).
var groupTypes = map[string][]store.MediaType{
	"movies-tv": {store.TypeMovie, store.TypeTV},
	"books":     {store.TypeBook},
	"games":     {store.TypeGame},
}

var groupLabels = map[string]string{
	"movies-tv": "Movies & TV", "books": "Books", "games": "Games",
}

func (s *site) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Store.Ping(r.Context()); err != nil {
		s.deps.Logger.Error("health check failed", "error", err)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// nav builds the layout model; total per group = sum across states.
func (s *site) nav(r *http.Request, active string) (Nav, error) {
	counts, err := s.deps.Store.GroupStateCounts(r.Context())
	if err != nil {
		return Nav{}, err
	}
	byGroup := map[string]int{}
	for group, types := range groupTypes {
		for _, mt := range types {
			for _, n := range counts[mt] {
				byGroup[group] += n
			}
		}
	}
	return Nav{Active: active, Counts: byGroup}, nil
}

// HomeRow is one entry in a landing panel.
type HomeRow struct {
	ID       int64
	Title    string
	Sub      string // genres line, or "now on X"
	Right    string // right-aligned annotation
	Group    string
	DotClass string // video | book | game
	Cover    *CoverRef
}

// HomeGroup is one type-grouped section inside a panel.
type HomeGroup struct {
	Label    string
	DotClass string
	Rows     []HomeRow
}

// LibLine is one line of the library-counts panel.
type LibLine struct {
	Group, Label, DotClass    string
	WantTo, InProgress, DoneN int
}

type HomeData struct {
	Nav      Nav
	Continue []HomeGroup
	Newly    []HomeGroup
	Library  []LibLine
}

// CoverRef renders either the real cover or the monogram placeholder.
type CoverRef struct {
	URL      string // "" ⇒ placeholder
	Monogram string
	Hue      int
}

func coverRef(it *store.MediaItem) *CoverRef {
	c := &CoverRef{Monogram: monogram(it.Title), Hue: hueFor(it.Title)}
	if it.CoverPath != nil {
		c.URL = "/" + *it.CoverPath // cover_path is "covers/{id}.jpg"
	}
	return c
}

func monogram(title string) string {
	words := strings.Fields(title)
	m := ""
	for i, w := range words {
		if i == 2 {
			break
		}
		r := []rune(w)
		m += strings.ToUpper(string(r[0]))
	}
	return m
}

// hueFor gives stable placeholder hues per title (same device as the
// M5 mock, minus its hand-picked values).
func hueFor(title string) int {
	h := 0
	for _, r := range title {
		h = (h*31 + int(r)) % 360
	}
	return h
}

func dotClassFor(mt store.MediaType) string {
	switch mt {
	case store.TypeBook:
		return "book"
	case store.TypeGame:
		return "game"
	default:
		return "video"
	}
}

func groupFor(mt store.MediaType) string {
	switch mt {
	case store.TypeBook:
		return "books"
	case store.TypeGame:
		return "games"
	default:
		return "movies-tv"
	}
}

// groupRows buckets items into the fixed group order, dropping empty
// groups — mirrors the mock's groupedPanel.
func groupRows(items []store.MediaItem, sub func(store.MediaItem) (string, string)) []HomeGroup {
	byGroup := map[string][]HomeRow{}
	for i := range items {
		it := &items[i]
		s, right := sub(*it)
		g := groupFor(it.MediaType)
		byGroup[g] = append(byGroup[g], HomeRow{
			ID: it.ID, Title: it.Title, Sub: s, Right: right,
			Group: g, DotClass: dotClassFor(it.MediaType), Cover: coverRef(it),
		})
	}
	var out []HomeGroup
	for _, g := range []string{"movies-tv", "books", "games"} {
		if rows := byGroup[g]; len(rows) > 0 {
			out = append(out, HomeGroup{Label: groupLabels[g], DotClass: map[string]string{
				"movies-tv": "video", "books": "book", "games": "game"}[g], Rows: rows})
		}
	}
	return out
}
