package server

import (
	"encoding/json"
	"math"
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

// stateOrder fixes the left-to-right order of the toolbar's state tabs;
// stateNames supplies their labels.
var stateOrder = []store.State{
	store.StateWantTo, store.StateInProgress, store.StateDone, store.StateAbandoned,
}

var stateNames = map[store.State]string{
	store.StateWantTo:     "Want to",
	store.StateInProgress: "In progress",
	store.StateDone:       "Done",
	store.StateAbandoned:  "Abandoned",
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

// TabRow is one row of the ledger table.
type TabRow struct {
	ID         int64
	Title      string
	Genres     string
	Type       store.MediaType
	Year       *int
	Rating     *int // avg across sources, nil when unrated
	Avail      []AvailBadge
	State      store.State
	StateLabel string
	Added      string
	DotClass   string
	Cover      *CoverRef
}

// AvailBadge is one availability chip in a row's Availability cell.
type AvailBadge struct {
	Label string
	Class string // "sub" | "own" | ""
}

// StateTab is one entry in the toolbar's state selector.
type StateTab struct {
	State  store.State
	Label  string
	Count  int
	Active bool
}

// TabData is the tab.html view model.
type TabData struct {
	Nav     Nav
	Group   string
	Label   string
	Sub     string // "" | "movie" | "tv"
	States  []StateTab
	Filter  store.ListFilter
	Rows    []TabRow
	Total   int
	Density string // s|m|l
}

// tabData builds the tab.html view model: state-tab counts (summed
// across the group's types, respecting sub when set), row density from
// settings, and per-row ratings/availability classified against a
// subscribed-services map fetched once.
func (s *site) tabData(r *http.Request, group, sub string, f store.ListFilter, items []store.MediaItem) (TabData, error) {
	ctx := r.Context()

	nav, err := s.nav(r, group)
	if err != nil {
		return TabData{}, err
	}

	counts, err := s.deps.Store.GroupStateCounts(ctx)
	if err != nil {
		return TabData{}, err
	}
	types := groupTypes[group]
	if sub != "" {
		types = []store.MediaType{store.MediaType(sub)}
	}
	states := make([]StateTab, 0, len(stateOrder))
	for _, st := range stateOrder {
		n := 0
		for _, mt := range types {
			n += counts[mt][st]
		}
		states = append(states, StateTab{State: st, Label: stateNames[st], Count: n, Active: f.State == st})
	}

	density, _, err := s.deps.Store.GetSetting(ctx, "row_density")
	if err != nil {
		return TabData{}, err
	}
	// Whitelist valid density values; default to "l" for invalid or unset
	switch density {
	case "s", "m", "l":
		// Valid
	default:
		density = "l"
	}

	services, err := s.deps.Store.ListServices(ctx)
	if err != nil {
		return TabData{}, err
	}
	svcByCode := make(map[string]store.Service, len(services))
	for _, sv := range services {
		svcByCode[sv.Slug] = sv
	}

	rows := make([]TabRow, 0, len(items))
	for i := range items {
		it := &items[i]

		ratings, err := s.deps.Store.GetRatings(ctx, it.ID)
		if err != nil {
			return TabData{}, err
		}
		var rating *int
		if len(ratings) > 0 {
			sum := 0
			for _, rt := range ratings {
				sum += rt.Score
			}
			avg := int(math.Round(float64(sum) / float64(len(ratings))))
			rating = &avg
		}

		avail, err := s.deps.Store.GetAvailability(ctx, it.ID)
		if err != nil {
			return TabData{}, err
		}
		badges := make([]AvailBadge, 0, len(avail))
		for _, a := range avail {
			sv := svcByCode[a.ServiceSlug]
			label := sv.Name
			if label == "" {
				label = a.ServiceSlug
			}
			class := ""
			switch {
			case a.Kind == store.KindOwned:
				class = "own"
			case sv.Subscribed:
				class = "sub"
			}
			badges = append(badges, AvailBadge{Label: label, Class: class})
		}

		added := it.AddedAt
		if len(added) > 10 {
			added = added[:10]
		}

		rows = append(rows, TabRow{
			ID:         it.ID,
			Title:      it.Title,
			Genres:     strings.Join(it.Genres, " · "),
			Type:       it.MediaType,
			Year:       it.ReleaseYear,
			Rating:     rating,
			Avail:      badges,
			State:      it.State,
			StateLabel: stateNames[it.State],
			Added:      added,
			DotClass:   dotClassFor(it.MediaType),
			Cover:      coverRef(it),
		})
	}

	return TabData{
		Nav:     nav,
		Group:   group,
		Label:   groupLabels[group],
		Sub:     sub,
		States:  states,
		Filter:  f,
		Rows:    rows,
		Total:   len(rows),
		Density: density,
	}, nil
}
