package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/varigg/mediatracker/internal/store"
)

// The handlers below are stubs. Tasks 4–5 replace each with the real
// implementation (and its own tests) on top of this foundation.

func (s *site) home(w http.ResponseWriter, r *http.Request) {
	nav, err := s.nav(r, "")
	if err != nil {
		s.fail(w, "home: nav", err)
		return
	}
	cont, err := s.deps.Store.ListItems(r.Context(), store.ListFilter{State: store.StateInProgress})
	if err != nil {
		s.fail(w, "home: continue", err)
		return
	}
	since := time.Now().Add(-s.deps.RefreshInterval)
	newly, err := s.deps.Store.NewlyAvailable(r.Context(), since)
	if err != nil {
		s.fail(w, "home: newly available", err)
		return
	}
	counts, err := s.deps.Store.GroupStateCounts(r.Context())
	if err != nil {
		s.fail(w, "home: counts", err)
		return
	}
	var lib []LibLine
	for _, g := range []string{"movies-tv", "books", "games"} {
		l := LibLine{Group: g, Label: groupLabels[g], DotClass: map[string]string{
			"movies-tv": "video", "books": "book", "games": "game"}[g]}
		for _, mt := range groupTypes[g] {
			l.WantTo += counts[mt][store.StateWantTo]
			l.InProgress += counts[mt][store.StateInProgress]
			l.DoneN += counts[mt][store.StateDone]
		}
		lib = append(lib, l)
	}
	data := HomeData{
		Nav: nav,
		Continue: groupRows(cont, func(it store.MediaItem) (string, string) {
			return strings.Join(it.Genres, " · "), "In progress"
		}),
		Newly: groupRows(newly, func(it store.MediaItem) (string, string) {
			return "newly available on a subscribed service", "this cycle"
		}),
		Library: lib,
	}
	if err := s.views.render(w, "home.html", data); err != nil {
		s.deps.Logger.Error("render home", "error", err)
	}
}

// fail logs and 500s — the system-failure path (spec §5 class 3).
func (s *site) fail(w http.ResponseWriter, op string, err error) {
	s.deps.Logger.Error(op, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// tab returns a handler for one of the three group tabs (movies-tv,
// books, games). The group constrains the media types in scope; for
// movies-tv only, a `type` query param further narrows to movie or tv.
func (s *site) tab(group string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := store.ParseListFilter(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if f.State == "" {
			f.State = store.StateWantTo
		}
		// The group constrains types; a type param further narrows
		// only within movies-tv.
		sub := ""
		if group == "movies-tv" && len(f.Types) == 1 &&
			(f.Types[0] == store.TypeMovie || f.Types[0] == store.TypeTV) {
			sub = string(f.Types[0])
		}
		if sub == "" {
			f.Types = groupTypes[group]
		}
		items, err := s.deps.Store.ListItems(r.Context(), f)
		if err != nil {
			s.fail(w, "tab: list", err)
			return
		}
		data, err := s.tabData(r, group, sub, f, items)
		if err != nil {
			s.fail(w, "tab: model", err)
			return
		}
		if r.Header.Get("HX-Request") == "true" {
			if err := s.views.renderBlock(w, "tab.html", "tab-body", data); err != nil {
				s.deps.Logger.Error("render tab fragment", "error", err)
			}
			return
		}
		if err := s.views.render(w, "tab.html", data); err != nil {
			s.deps.Logger.Error("render tab", "error", err)
		}
	}
}

func (s *site) detail(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (s *site) cover(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
