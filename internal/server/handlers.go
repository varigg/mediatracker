package server

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
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
	id, ok := s.itemID(w, r)
	if !ok {
		return
	}
	data, err := s.detailData(r, id, r.URL.Query().Get("flash"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.fail(w, "detail: model", err)
		return
	}
	if err := s.views.render(w, "detail.html", data); err != nil {
		s.deps.Logger.Error("render detail", "error", err)
	}
}

// itemID parses the path id; writes 400 and returns false on garbage.
func (s *site) itemID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad item id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// respondDetail re-renders the detail-body fragment (HTMX) or 303s back
// to the detail page (plain form post) after a successful mutation.
func (s *site) respondDetail(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, fmt.Sprintf("/items/%d", id), http.StatusSeeOther)
		return
	}
	data, err := s.detailData(r, id, "")
	if err != nil {
		s.fail(w, "detail refresh", err)
		return
	}
	if err := s.views.renderBlock(w, "detail.html", "detail-body", data); err != nil {
		s.deps.Logger.Error("render detail fragment", "error", err)
	}
}

// badRequest answers a user-input error: an inline-error fragment
// swapped into the target for HTMX requests, plain text otherwise.
func (s *site) badRequest(w http.ResponseWriter, r *http.Request, msg string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		if err := s.views.executeBlock(w, "detail.html", "inline-error", msg); err != nil {
			s.deps.Logger.Error("render inline error", "error", err)
		}
		return
	}
	http.Error(w, msg, http.StatusBadRequest)
}

// updateState handles POST /items/{id}/state (form: to=<state>).
func (s *site) updateState(w http.ResponseWriter, r *http.Request) {
	id, ok := s.itemID(w, r)
	if !ok {
		return
	}
	to := store.State(r.FormValue("to"))
	switch to {
	case store.StateWantTo, store.StateInProgress, store.StateDone, store.StateAbandoned:
	default:
		s.badRequest(w, r, "unknown state: "+string(to))
		return
	}
	if err := s.deps.Store.UpdateState(r.Context(), id, to); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			http.NotFound(w, r)
		case errors.Is(err, store.ErrIllegalTransition):
			s.badRequest(w, r, err.Error())
		default:
			s.fail(w, "update state", err)
		}
		return
	}
	s.respondDetail(w, r, id)
}

// updateReview handles POST /items/{id}/review (form: verdict=,
// optional completed_at= defaulting to today).
func (s *site) updateReview(w http.ResponseWriter, r *http.Request) {
	id, ok := s.itemID(w, r)
	if !ok {
		return
	}
	v := store.Verdict(r.FormValue("verdict"))
	switch v {
	case store.VerdictLiked, store.VerdictOK, store.VerdictDisliked:
	default:
		s.badRequest(w, r, "unknown verdict: "+string(v))
		return
	}
	completedAt := r.FormValue("completed_at")
	if completedAt == "" {
		completedAt = time.Now().Format("2006-01-02")
	}
	if err := s.deps.Store.UpdateReview(r.Context(), id, v, completedAt); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			http.NotFound(w, r)
		case errors.Is(err, store.ErrNotTerminal):
			s.badRequest(w, r, err.Error())
		default:
			s.fail(w, "update review", err)
		}
		return
	}
	s.respondDetail(w, r, id)
}

// updateNotes handles PUT /items/{id}/notes (form: notes=).
func (s *site) updateNotes(w http.ResponseWriter, r *http.Request) {
	id, ok := s.itemID(w, r)
	if !ok {
		return
	}
	if err := s.deps.Store.UpdateNotes(r.Context(), id, r.FormValue("notes")); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			http.NotFound(w, r)
		default:
			s.fail(w, "update notes", err)
		}
		return
	}
	s.respondDetail(w, r, id)
}

// previewNotes handles POST /items/{id}/notes/preview (form: notes=):
// renders the markdown preview fragment without touching the store.
func (s *site) previewNotes(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.itemID(w, r); !ok {
		return
	}
	html, err := renderMarkdown(r.FormValue("notes"))
	if err != nil {
		s.fail(w, "preview notes", err)
		return
	}
	if err := s.views.renderBlock(w, "detail.html", "notes-preview", html); err != nil {
		s.deps.Logger.Error("render notes preview", "error", err)
	}
}

// coverName is the only shape covers/ serves: "{item id}.jpg", checked
// before the filesystem is touched (also blocks path traversal).
var coverName = regexp.MustCompile(`^[0-9]+\.jpg$`)

func (s *site) cover(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !coverName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.deps.DataDir, "covers", name))
}
