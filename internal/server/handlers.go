package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/varigg/mediatracker/internal/ingest"
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

// refreshItem handles POST /items/{id}/refresh: a manual per-item
// refresh via the same logic RunCycle uses per item.
func (s *site) refreshItem(w http.ResponseWriter, r *http.Request) {
	id, ok := s.itemID(w, r)
	if !ok {
		return
	}
	if _, err := s.deps.Refresher.RefreshItem(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, ingest.ErrItemNotActive):
			s.badRequest(w, r, err.Error())
		case errors.Is(err, store.ErrNotFound):
			http.NotFound(w, r)
		default:
			s.fail(w, "refresh item", err)
		}
		return
	}
	s.respondDetail(w, r, id)
}

// refreshAll handles POST /refresh: kicks off an async global refresh
// cycle if one isn't already running. It responds immediately (202)
// and never blocks on the cycle itself.
func (s *site) refreshAll(w http.ResponseWriter, r *http.Request) {
	s.refreshMu.Lock()
	if s.refreshing {
		s.refreshMu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("<span>Refresh already running</span>"))
		return
	}
	s.refreshing = true
	s.refreshMu.Unlock()

	if s.deps.Background != nil {
		s.deps.Background.Add(1)
	}
	go func() {
		defer func() {
			if s.deps.Background != nil {
				defer s.deps.Background.Done()
			}
			s.refreshMu.Lock()
			s.refreshing = false
			s.refreshMu.Unlock()
		}()
		// context.Background(), not r.Context(): the cycle must outlive
		// this request — it keeps running after the response is sent.
		if _, err := s.deps.Refresher.RunCycle(context.Background()); err != nil {
			s.deps.Logger.Error("global refresh cycle failed", "error", err)
		}
	}()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("<span>Refresh started</span>"))
}

// validMediaType reports whether typ is one of the four types the
// add-flow (and the registry) knows about.
func validMediaType(typ store.MediaType) bool {
	switch typ {
	case store.TypeMovie, store.TypeTV, store.TypeBook, store.TypeGame:
		return true
	default:
		return false
	}
}

// upstreamError answers a provider/upstream failure (spec §5 class 1:
// the request was well-formed, but a dependency we don't control
// failed) — 502, distinct from s.fail's 500 which is reserved for our
// own system failures (class 3). HTMX gets the same inline-error
// fragment badRequest uses for 4xx; a plain request gets a 502 body.
func (s *site) upstreamError(w http.ResponseWriter, r *http.Request, msg string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		if err := s.views.executeBlock(w, "detail.html", "inline-error", msg); err != nil {
			s.deps.Logger.Error("render inline error", "error", err)
		}
		return
	}
	http.Error(w, msg, http.StatusBadGateway)
}

// search handles GET /search?type=&q= — the search-box's live picker.
// It always renders the search-results fragment (or, for an empty
// query, nothing): there is no full-page variant of this endpoint.
func (s *site) search(w http.ResponseWriter, r *http.Request) {
	mediaType := store.MediaType(r.URL.Query().Get("type"))
	if !validMediaType(mediaType) {
		http.Error(w, "unknown media type: "+string(mediaType), http.StatusBadRequest)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return
	}

	p, err := s.deps.Ingest.Registry.Get(mediaType)
	if err != nil {
		// Missing provider config is user-input-adjacent (an admin setup
		// gap, not a request failure): 200 with a hint row, not an error.
		data := SearchData{Hint: fmt.Sprintf("No provider configured for %s — add the API key to config.toml.", mediaType)}
		if err := s.views.renderBlock(w, "search.html", "search-results", data); err != nil {
			s.deps.Logger.Error("render search hint", "error", err)
		}
		return
	}

	candidates, err := p.Search(r.Context(), q)
	if err != nil {
		s.upstreamError(w, r, fmt.Sprintf("search failed: %v", err))
		return
	}
	if len(candidates) > 8 {
		candidates = candidates[:8]
	}
	data := SearchData{Candidates: make([]SearchCandidate, 0, len(candidates))}
	for _, c := range candidates {
		data.Candidates = append(data.Candidates, toSearchCandidate(c))
	}
	if err := s.views.renderBlock(w, "search.html", "search-results", data); err != nil {
		s.deps.Logger.Error("render search results", "error", err)
	}
}

// addItem handles POST /items (form: type=, provider_id=) — the picker
// button's target. On success it flashes "added" or "duplicate"
// depending on ingest.Deps.Add's created flag and either sets
// HX-Redirect (HTMX) or 303s (plain form post) to the new detail page.
func (s *site) addItem(w http.ResponseWriter, r *http.Request) {
	mediaType := store.MediaType(r.FormValue("type"))
	if !validMediaType(mediaType) {
		s.badRequest(w, r, "unknown media type: "+string(mediaType))
		return
	}
	providerID := r.FormValue("provider_id")
	if providerID == "" {
		s.badRequest(w, r, "missing provider_id")
		return
	}
	if _, err := s.deps.Ingest.Registry.Get(mediaType); err != nil {
		s.badRequest(w, r, fmt.Sprintf("no provider configured for %s", mediaType))
		return
	}

	// The add-flow's synchronous budget (spec §3): hydrate, persist, and
	// best-effort enrichment must land within this window.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	item, created, err := s.deps.Ingest.Add(ctx, mediaType, providerID)
	if err != nil {
		if errors.Is(err, ingest.ErrHydrate) {
			s.upstreamError(w, r, fmt.Sprintf("add failed: %v", err))
			return
		}
		s.fail(w, "add item", err)
		return
	}

	flash := "duplicate"
	if created {
		flash = "added"
	}
	target := fmt.Sprintf("/items/%d?flash=%s", item.ID, flash)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// settings handles GET /settings: the full Settings page (services,
// providers, display density, refresh).
func (s *site) settings(w http.ResponseWriter, r *http.Request) {
	data, err := s.settingsData(r)
	if err != nil {
		s.fail(w, "settings: model", err)
		return
	}
	if err := s.views.render(w, "settings.html", data); err != nil {
		s.deps.Logger.Error("render settings", "error", err)
	}
}

// respondSettings re-renders the settings-body fragment (HTMX) or 303s
// back to /settings (plain form post) after a successful mutation —
// mirrors respondDetail.
func (s *site) respondSettings(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	data, err := s.settingsData(r)
	if err != nil {
		s.fail(w, "settings refresh", err)
		return
	}
	if err := s.views.renderBlock(w, "settings.html", "settings-body", data); err != nil {
		s.deps.Logger.Error("render settings fragment", "error", err)
	}
}

// toggleService handles POST /settings/services (form: slug=): flips the
// named service's subscribed flag. Unknown slug -> 404.
func (s *site) toggleService(w http.ResponseWriter, r *http.Request) {
	slug := r.FormValue("slug")
	services, err := s.deps.Store.ListServices(r.Context())
	if err != nil {
		s.fail(w, "toggle service: list", err)
		return
	}
	var current bool
	found := false
	for _, sv := range services {
		if sv.Slug == slug {
			current = sv.Subscribed
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	if err := s.deps.Store.SetServiceSubscribed(r.Context(), slug, !current); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.fail(w, "toggle service", err)
		return
	}
	s.respondSettings(w, r)
}

// setDensity handles POST /settings/density (form: density=s|m|l).
func (s *site) setDensity(w http.ResponseWriter, r *http.Request) {
	d := r.FormValue("density")
	switch d {
	case "s", "m", "l":
	default:
		s.badRequest(w, r, "unknown density: "+d)
		return
	}
	if err := s.deps.Store.SetSetting(r.Context(), "row_density", d); err != nil {
		s.fail(w, "set density", err)
		return
	}
	s.respondSettings(w, r)
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
