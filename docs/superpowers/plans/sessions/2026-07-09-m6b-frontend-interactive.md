# M6b — Frontend Interactions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** The app becomes fully usable end to end: lifecycle transitions,
verdict + notes editing with Markdown preview, per-item and global
refresh, the debounced add flow with duplicate-redirect + flash, and the
settings page (service subscriptions, provider key status, density
toggle, refresh-now) — closing Milestone 6 and deleting the `/debug/*`
scaffolding.

**Architecture:** `server.Deps` grows the mutation-side dependencies
(`ingest.Deps` for the add flow, `*ingest.Refresher` for refresh
endpoints, a `ProviderStatus` of booleans for the settings page — never
raw keys). Mutations follow the established HTMX pattern: forms
`hx-post`/`hx-put` to their endpoint, the handler re-renders the
affected fragment (or sends `HX-Redirect` for navigation). User-input
failures map 4xx per the spec's taxonomy: `ErrIllegalTransition`/
`ErrNotTerminal`/bad verdicts → 400 with an inline error fragment.

**Read first:** spec Section 4 (route contracts) + Section 3 (add-flow
budget), M5 addendum (density toggle as persisted preference), and the
M6a session plan's foundation (Deps/views/models patterns to extend).

## Global Constraints

- No new Go dependencies. No CDN. Everything embedded.
- Add flow: 10-second budget via `context.WithTimeout` around
  `ingest.Add` (spec §3; the M4 plan deferred the deadline to this HTTP
  layer). Only primary-hydrate failure aborts; the handler surfaces it
  as a 502-style inline error, not a 500 (provider failure ≠ system
  failure).
- Duplicate add navigates to the existing item with a flash (spec §4).
- Notes: explicit save, no autosave; preview is a server-rendered
  fragment through the same goldmark path as display (one renderer).
- Raw API keys never reach `server` — the settings page gets booleans.
- The temporary `/debug/*` routes are deleted this session, and the
  layout's search box comes alive.
- Tests: `httptest` + seeded real-SQLite temp DB + stub providers via
  the registry (milestone key tests: add flow end-to-end, transition
  rejections, subscription toggle affecting available-to-me).
- Errors `"pkg: op: %w"`; injected logger; Conventional Commits; no AI
  attribution.

## Design Decisions (flagged for sign-off)

1. **`server.Deps` gains `Ingest ingest.Deps`, `Refresher
   *ingest.Refresher`, and `Providers ProviderStatus`** (a struct of
   five booleans built in `main` from config — key *presence*, never
   values). The registry for `/search` is reached through
   `Ingest.Registry`; no duplicate wiring.
2. **Mutation responses re-render fragments, not full pages.** Detail
   mutations (`state`, `review`, `notes`) return the updated detail
   `content` block with an `HX-Retarget`-free simple swap (the whole
   `#detail-body` div becomes the swap target); the add flow responds
   with `HX-Redirect` to `/items/{id}?flash=added|duplicate` since it
   navigates. Non-HTMX fallbacks: 303 See Other redirects, so plain
   form posts still work.
3. **Flash is a query param** (`?flash=added|duplicate`), rendered as a
   dismissable banner by the detail template; no cookies/sessions.
4. **Global refresh-now runs async**: `POST /refresh` kicks
   `Refresher.RunCycle` in a goroutine (guarded by a mutex so
   concurrent clicks don't stack cycles) and immediately re-renders the
   settings fragment with "refresh started"; per-item refresh runs
   synchronously (single item, bounded work) and re-renders detail.
5. **Settings page scope (M6, not M7):** service checklist with
   instant-toggle checkboxes, provider key status booleans, density
   radio (s/m/l → `row_density` setting), last-refresh timestamp
   (`last_refresh_at` setting), refresh-now button. Per-provider
   health/snapshot ages remain M7.
6. **Density toggle lives on the settings page only** (not the tab
   toolbar): the M5 prototype's toolbar toggle was an exploration
   device; as a persisted preference it's settings-shaped. (Cheap to
   add to the toolbar later if it's missed.)
7. **Search debounce** is htmx-native: `hx-trigger="input changed
   delay:300ms"` on the top-bar input targeting a dropdown container;
   `GET /search?type=&q=` renders the candidate list fragment. Type is
   chosen by a small select next to the input (defaulting to the active
   tab's group when on a tab page — that refinement is template logic
   only). Media groups with no configured provider render a "not
   configured" hint (registry returns an error → fragment says so).
8. **M6a-deferred polish folded into the final task**: toolbar
   filter-state preservation (wire all tab-toolbar links to carry
   genre/available/sort/dir consistently), shared default-direction
   helper (`store.DefaultDir(sort)`) consumed by `sortLink`, dead
   `HomeRow.Group` removal + stale comment fix + dot-class map dedup.

## File Structure

```
internal/server/
  server.go              (modify) new routes, Deps fields, ProviderStatus
  handlers.go            (modify) mutation/search/settings/refresh handlers
  models.go              (modify) detail flash + settings/search models
  views.go               (modify) parse settings.html + search partial set
  templates/detail.html  (modify) live forms, #detail-body swap target, flash
  templates/layout.html  (modify) live search box + dropdown
  templates/settings.html (new)
  templates/search.html  (new) candidate-list fragment (fragment-only set)
  templates/tab.html     (modify, Task 5 polish) param preservation
  web_test.go            (modify) mutation/add/settings tests + stub registry
internal/store/
  query.go               (modify, Task 5) DefaultDir helper
cmd/mediatracker/
  main.go                (modify) new Deps fields; delete registerDebugRoutes
```

---

### Task 1: Item Mutations — State, Review, Notes (+ Preview)

**Files:**
- Modify: `internal/server/server.go` (routes), `handlers.go`,
  `models.go`, `templates/detail.html`, `web_test.go`

**Interfaces:**
- Consumes: `store.UpdateState` (`ErrIllegalTransition`, `ErrNotFound`),
  `store.UpdateReview` (`ErrNotTerminal`), `store.UpdateNotes`,
  `renderMarkdown`, M6a's `detailData`.
- Produces routes: `POST /items/{id}/state` (form `to=<state>`),
  `POST /items/{id}/review` (form `verdict=`, optional `completed_at=`
  defaulting to today), `PUT /items/{id}/notes` (form `notes=`),
  `POST /items/{id}/notes/preview` (form `notes=`, renders preview
  fragment; does NOT save).
- Detail template rework: everything under the `back` link wraps in
  `<div id="detail-body">…</div>` and moves into a
  `{{define "detail-body"}}` block; transition buttons become live
  `hx-post` forms; verdict pills live `hx-post`; notes textarea +
  Preview (`hx-post …/notes/preview`, target `#notes-preview`) + Save
  (`hx-put …/notes`); a `.flash` banner div renders when
  `DetailData.Flash != ""` (CSS: reuse `.avchip.sub` tint style — add a
  small `.flash` rule to app.css).
- Mutation handlers on success re-render the `detail-body` block for
  HTMX requests, 303-redirect to `/items/{id}` otherwise. User-input
  errors (illegal transition, bad verdict, not-terminal) → 400 with a
  small inline error fragment `{{define "inline-error"}}` rendered into
  the swap target; `ErrNotFound` → 404.

- [ ] **Step 1: Write the failing tests** (append to `web_test.go`)

```go
func postForm(t *testing.T, srv *httptest.Server, method, path string, form url.Values, htmx bool) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestTransitionHappyPath(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	resp, body := postForm(t, srv, "POST", fmt.Sprintf("/items/%d/state", ids["game"]),
		url.Values{"to": {"in_progress"}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "In progress") {
		t.Error("fragment must reflect the new state")
	}
	it, err := st.GetItem(context.Background(), ids["game"])
	if err != nil || it.State != store.StateInProgress {
		t.Errorf("state = %v, err %v", it.State, err)
	}
}

func TestTransitionRejectsIllegal(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	// book is done: done→abandoned is illegal (only done→in_progress).
	resp, _ := postForm(t, srv, "POST", fmt.Sprintf("/items/%d/state", ids["book"]),
		url.Values{"to": {"abandoned"}}, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	it, _ := st.GetItem(context.Background(), ids["book"])
	if it.State != store.StateDone {
		t.Error("illegal transition must not change state")
	}
}

func TestTransitionUnknownStateAndItem(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	if resp, _ := postForm(t, srv, "POST", fmt.Sprintf("/items/%d/state", ids["game"]),
		url.Values{"to": {"vaporized"}}, true); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown state: %d, want 400", resp.StatusCode)
	}
	if resp, _ := postForm(t, srv, "POST", "/items/99999/state",
		url.Values{"to": {"done"}}, true); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown item: %d, want 404", resp.StatusCode)
	}
}

func TestReviewOnTerminalItem(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	resp, body := postForm(t, srv, "POST", fmt.Sprintf("/items/%d/review", ids["book"]),
		url.Values{"verdict": {"liked"}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, body)
	}
	// non-terminal rejection
	resp, _ = postForm(t, srv, "POST", fmt.Sprintf("/items/%d/review", ids["tv"]),
		url.Values{"verdict": {"liked"}}, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("non-terminal review: %d, want 400", resp.StatusCode)
	}
}

func TestNotesSaveAndPreview(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	resp, _ := postForm(t, srv, "PUT", fmt.Sprintf("/items/%d/notes", ids["game"]),
		url.Values{"notes": {"**bold** move"}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save: status = %d", resp.StatusCode)
	}
	it, _ := st.GetItem(context.Background(), ids["game"])
	if it.Notes != "**bold** move" {
		t.Errorf("notes = %q", it.Notes)
	}
	// preview renders markdown but must NOT save
	resp, body := postForm(t, srv, "POST", fmt.Sprintf("/items/%d/notes/preview", ids["game"]),
		url.Values{"notes": {"*draft*"}}, true)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "<em>draft</em>") {
		t.Fatalf("preview: %d %s", resp.StatusCode, body)
	}
	it, _ = st.GetItem(context.Background(), ids["game"])
	if it.Notes != "**bold** move" {
		t.Error("preview must not persist")
	}
}

func TestNonHTMXMutationRedirects(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	resp, _ := postForm(t, srv, "POST", fmt.Sprintf("/items/%d/state", ids["game"]),
		url.Values{"to": {"in_progress"}}, false)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run to verify failure** (`go test ./internal/server/
  -run 'TestTransition|TestReview|TestNotes|TestNonHTMX'` → 404s/failures)

- [ ] **Step 3: Implement**

Routes in `server.go`:

```go
	mux.HandleFunc("POST /items/{id}/state", s.updateState)
	mux.HandleFunc("POST /items/{id}/review", s.updateReview)
	mux.HandleFunc("PUT /items/{id}/notes", s.updateNotes)
	mux.HandleFunc("POST /items/{id}/notes/preview", s.previewNotes)
```

Handlers share a helper:

```go
// itemID parses the path id; writes 400 and returns false on garbage.
func (s *site) itemID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad item id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// respondDetail re-renders the detail-body fragment (HTMX) or 303s
// back to the detail page (plain form post).
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
```

(M6a's `detailData` gains a `flash string` parameter — update its one
existing call site in the `detail` handler to pass
`r.URL.Query().Get("flash")`.)

`updateState`: parse `to` via a `switch store.State(...)` whitelist
(unknown → 400); call `UpdateState`; `errors.Is ErrNotFound` → 404,
`ErrIllegalTransition` → 400 (body: the error text), else 500;
success → `respondDetail`. `updateReview`: verdict whitelist; optional
`completed_at` (default today `time.Now().Format("2006-01-02")`);
`ErrNotTerminal` → 400, `ErrNotFound` → 404. `updateNotes`:
`UpdateNotes`, same mapping, `respondDetail`. `previewNotes`: does not
touch the store; `renderMarkdown(r.FormValue("notes"))` → write the
fragment `<div class="nprev">…</div>` directly (no template needed —
but use a tiny `{{define "notes-preview"}}` in detail.html for
consistency; data = `template.HTML`).

Template rework in `detail.html`: wrap post-back content in
`{{define "detail-body"}}`; content block becomes
`<div id="detail-body">{{template "detail-body" .}}</div>`. Transition
buttons:

```html
<form hx-post="/items/{{$.Item.ID}}/state" hx-target="#detail-body" style="display:inline">
  <input type="hidden" name="to" value="{{.To}}">
  <button class="tbtn {{if .Primary}}primary{{end}}">{{.Label}}</button>
</form>
```

Verdict pills likewise (`hx-post …/review`, hidden `verdict`). Notes:

```html
<textarea id="ntext" name="notes" form="notesform">{{.Item.Notes}}</textarea>
<div id="notes-preview">{{if .NotesHTML}}<div class="nprev">{{.NotesHTML}}</div>{{end}}</div>
<form id="notesform" class="nbar">
  <button class="tbtn" hx-post="/items/{{.Item.ID}}/notes/preview" hx-target="#notes-preview" hx-include="#ntext">Preview</button>
  <button class="tbtn primary" hx-put="/items/{{.Item.ID}}/notes" hx-target="#detail-body" hx-include="#ntext">Save notes</button>
  <span class="savehint">Markdown · explicit save, no autosave</span>
</form>
```

Flash banner at the top of `detail-body` when `.Flash` is `added` /
`duplicate` ("Added to your library." / "Already in your library —
here it is."). app.css gains:

```css
.flash{background:var(--accent-tint);color:var(--accent-ink);border-radius:9px;
  padding:9px 14px;margin-bottom:14px;font-weight:600}
```

Remove every `disabled title="Interactive in the next milestone"` from
detail.html.

- [ ] **Step 4: verify pass** · **Step 5: fmt/vet/full suite, commit**
  `feat: wire item mutations (state, review, notes with preview)`

---

### Task 2: Refresh Endpoints

**Files:**
- Modify: `server.go` (Deps + routes), `handlers.go`, `web_test.go`,
  `cmd/mediatracker/main.go`, `templates/detail.html`

**Interfaces:**
- `server.Deps` gains `Refresher *ingest.Refresher`. `main.go` passes
  the one it already constructs.
- Produces: `POST /items/{id}/refresh` — calls
  `Refresher.RefreshItem`; `ErrItemNotActive` → 400,
  `store.ErrNotFound` → 404; success → `respondDetail`. `POST /refresh`
  — spawns `RunCycle` in a goroutine if none is running (mutex + bool
  on `site`), responds 202 + fragment "Refresh started" (this endpoint
  is consumed by the settings page in Task 4; a plain 202 text response
  is fine until then and stays fine after — the settings page targets
  it with `hx-swap="innerHTML"` on a status span).
- Detail template: a small "Refresh now" button in the Details section
  (`hx-post`, target `#detail-body`).
- Tests: per-item refresh on an active item 200s and bumps
  `refreshed_at` (stub `Refresher` needs real `ingest.Deps` — build one
  with an empty registry and no availability providers: hydrate is
  skipped (no provider → logged skip), availability upsert no-ops, but
  `TouchRefreshed` still runs — that's the observable); frozen item →
  400; `POST /refresh` → 202 and doesn't panic with the same stub;
  double-post while running → still 202 (idempotent-ish, "already
  running" text variant acceptable).

Construction in `newTestServer` changes: build
`ingest.NewRefresher(ingest.Deps{Store: st, Registry:
providers.NewRegistry(), Logger: …, DataDir: dataDir}, time.Hour)` and
pass it in Deps. (Import `internal/providers` + `internal/ingest` in
the test file.)

Steps: failing tests → implement → pass → commit
`feat: add per-item and global refresh endpoints`.

---

### Task 3: Add Flow — Search Partial & POST /items

**Files:**
- Modify: `server.go` (Deps.Ingest + routes), `handlers.go`,
  `models.go`, `views.go` (search fragment set), `templates/layout.html`,
  `web_test.go`; Create: `templates/search.html`

**Interfaces:**
- `server.Deps` gains `Ingest ingest.Deps` (registry reached via
  `Ingest.Registry`). `main.go` passes the `deps` it already builds for
  the refresher.
- `GET /search?type={movie|tv|book|game}&q=…` → candidate-list
  fragment: up to 8 `providers.Candidate`s as buttons that
  `hx-post="/items"` with hidden `type` + `provider_id`; unknown/empty
  type → 400; registry "not registered" error → 200 with a "provider
  not configured" hint row (user-input-adjacent, not a failure);
  upstream search error → 502 with inline error row. Empty `q` → empty
  200 fragment.
- `POST /items` (form `type`, `provider_id`) → `context.WithTimeout(r.
  Context(), 10*time.Second)`; `ingest.Add`; on success `HX-Redirect`
  header (HTMX) or 303 to `/items/{id}?flash=added`; when `Add`
  returned an existing item (re-add) → `flash=duplicate`. **Interface
  change (two birds, user-approved):** `ingest.Add` currently (a)
  doesn't expose created-vs-existing and (b) is the package's lone free
  function while `Refresher` uses methods — the long-deferred style
  item. Convert it to a method on `Deps` with the created flag:
  `func (d Deps) Add(ctx context.Context, mediaType store.MediaType,
  providerID string) (*store.MediaItem, bool, error)`. One churn covers
  both. Call sites: the ingest tests, M4's debug route in main.go
  (deleted in Task 5 — update minimally now), and the new handler here
  (`s.deps.Ingest.Add(...)`). Hydrate failure → 502 inline error;
  registry miss → 400.
- `templates/search.html` is a **fragment-only template set** (no
  layout): parse it standalone in `newViews` as `fragments` and give
  `renderBlock` access — simplest: parse `search.html` into its own
  `*template.Template` stored as `v.pages["search.html"]` and render
  its `search-results` define via the existing `renderBlock`.
- `layout.html`: enable the search box —

```html
<div class="searchbox">
  <span class="glyph">⌕</span>
  <select id="qtype" name="type">
    <option value="movie">Movie</option><option value="tv">TV</option>
    <option value="book">Book</option><option value="game">Game</option>
  </select>
  <input id="q" name="q" placeholder="Add a movie, book, game…" autocomplete="off"
    hx-get="/search" hx-include="#qtype" hx-trigger="input changed delay:300ms"
    hx-target="#search-results">
  <div class="search-pop" id="search-results"></div>
</div>
```

  plus a tiny app.css tweak so `.search-pop` opens when non-empty
  (`.search-pop:not(:empty){display:block}` replacing the `.open`
  mechanism) and a narrow `#qtype` select style consistent with `.sel`.
- Tests (stub `providers.MetadataProvider` registered for movie in a
  registry inside `Deps.Ingest`): search fragment renders candidates;
  `POST /items` HTMX → `HX-Redirect: /items/{id}?flash=added`, item +
  ratings persisted, cover downloaded from an `httptest` image server
  (reuse M4's `fakeJPEGForTest` pattern — copy the tiny JPEG helper
  into `web_test.go`); duplicate POST → `flash=duplicate`; detail page
  with `?flash=duplicate` renders the banner; hydrate-failure stub →
  502; `/search?type=game` with empty registry → "not configured" hint.

Steps: failing tests → implement (including the `ingest.Add` signature
change + its test updates) → pass → commit
`feat: add search partial and add-from-candidate flow`.

---

### Task 4: Settings Page

**Files:**
- Modify: `server.go` (ProviderStatus + routes), `handlers.go`,
  `models.go`, `views.go`, `main.go`, `web_test.go`;
  Create: `templates/settings.html`

**Interfaces:**
- `type ProviderStatus struct { TMDB, OMDB, IGDB, Hardcover, Steam bool }`
  on `server.Deps`; built in `main` from `cfg.Providers` key presence.
- `GET /settings`: nav-integrated page (add a Settings link to the
  layout top bar, far right next to the searchbox) rendering:
  - **Services** checklist grouped by media kind (`ListServices`):
    each `<input type=checkbox hx-post="/settings/services"
    hx-vals='{"slug":"…"}' hx-target="#settings-body">` toggling
    subscription.
  - **Providers**: five rows, "configured ✓ / not configured —" from
    `ProviderStatus`.
  - **Display**: density radio s/m/l → `hx-post /settings/density`.
  - **Refresh**: last-refresh timestamp (`GetSetting("last_refresh_at")`,
    "never" fallback), "Refresh now" button → `POST /refresh` target a
    status span.
  All inside `{{define "settings-body"}}` with `<div id="settings-body">`
  wrapper (same fragment pattern as detail/tab).
- `POST /settings/services` (form `slug`): flip = read current via
  `ListServices`, `SetServiceSubscribed(slug, !current)`; unknown slug
  (`ErrNotFound`) → 404; re-render settings-body.
- `POST /settings/density` (form `density` whitelisted s|m|l else 400):
  `SetSetting("row_density", v)`; re-render settings-body.
- Tests: settings renders services + provider status; toggle flips
  subscription **and** affects available-to-me (milestone key test:
  seed the netflix-available movie, `available=1` excludes it while
  unsubscribed→ toggle → included — drive the toggle through the HTTP
  endpoint, then GET `/movies-tv?available=1`); density POST persists
  and tab honors it; bad density → 400; unknown slug → 404.

Steps: failing tests → implement → pass → commit
`feat: add settings page with subscriptions, density, and refresh controls`.

---

### Task 5: Debug-Route Removal, Polish & M6a Deferred Cleanup

**Files:**
- Modify: `cmd/mediatracker/main.go`, `internal/store/query.go`,
  `internal/server/views.go`, `models.go`, `handlers.go`,
  `templates/tab.html`, `web_test.go` (only if assertions touch changed
  markup)

**Work items (each small, one commit total):**
1. Delete `registerDebugRoutes` + call site + now-unused imports from
   `main.go` (the real routes replaced every debug endpoint).
2. `store.DefaultDir(sort string) string` exported next to
   `buildListQuery` (single source for title→asc-else-desc);
   `buildListQuery` and `sortLink` both consume it.
3. Toolbar filter-state preservation: state tabs / subtabs /
   available-checkbox / sort headers all carry the full current state
   (state, type, genre, available, sort, dir) minus the dimension they
   change. Implement one Go helper `tabURL(d TabData, overrides
   map[string]string) string` used by all toolbar links via FuncMap
   (replaces the ad hoc string building), with unit tests pinning
   preservation (including the previously-missed `.Path` assertion).
   Sort headers become `<a>`s with real `href`s from the same helper
   (plain-link fallback parity with the rest of the toolbar —
   user-approved M6a-deferred fold-in).
4. Dead `HomeRow.Group` removed; `HomeRow.Sub` comment fixed;
   dot-class literal deduplicated into one package-level
   `groupDotClass` map used everywhere.
5. Full suite + fmt/vet.

Commit: `refactor: remove debug routes, share sort defaults, preserve toolbar state`.

---

### Task 5.5 (unplanned): Genre Filter

A post-Task-5 review surfaced that spec §4's "genre filter from present
genres" was never wired up: `ListFilter.Genre`, `ParseListFilter`'s
`genre` param, and the SQL `EXISTS (SELECT 1 FROM json_each(...))`
clause all existed and `tabURL` already round-tripped `genre` through
every toolbar control, but there was no way to enumerate which genres
were actually present for a given group/sub/state scope and no `<select>`
in the toolbar to drive it. Closed with `store.DistinctGenres(ctx, types,
state)` (a `DISTINCT ... FROM media_items, json_each(genres)` query
sharing `buildListQuery`'s type/state WHERE construction), a `TabData.
Genres []string` populated in `tabData`, and a toolbar `<select
name="genre">` between the subtabs and the available-to-me checkbox that
drives navigation via `hx-include="this"` against a `tabURL` override
that strips `genre` from the base href — so the select's own value is
the sole genre source while every other filter dimension survives.

---

### Task 6: Integration Sweep & Boot Smoke

1. CI parity: `gofmt -l . && go vet ./... && go build ./... && go test
   ./... -race -count=1 -coverprofile=coverage.out && go run
   github.com/mach6/go-covercheck/cmd/go-covercheck@v0.6.1 --no-color
   coverage.out` — all green; floors are ratchets, fix gaps not floors.
2. Boot smoke (fresh temp dir, port 18091): `GET /` 200; `GET
   /settings` 200 contains "not configured"; `POST /settings/density`
   density=m then `GET /games` contains `density-m`; `POST
   /items/1/state` → 404 (empty db); `GET /debug/search` → 404 (gone);
   clean SIGTERM exit 0.
3. Update ledger; done.

## Deferred beyond M6 (recorded, not lost)

Phone-layout fine-tuning (M6 polish window or M7), per-provider
health/snapshot ages on settings (M7), staleness markers (M7),
Stale availability rows (additive-only upsert — a title leaving a
service keeps its row; visible via available-to-me; needs
replace-per-enricher-scope refresh semantics) and batched tab-view
ratings/availability queries (single-user N+1, harmless today).

## Self-Review Notes

Spec §4 route table now fully covered: every route exists after Task 4
(landing/tabs/detail/covers/search from M6a+T3, items+state/review/
notes from T1, refresh×2 from T2, settings×2 from T4). §3's 10s add
budget → T3. Milestone key tests all present: add flow end-to-end (T3),
transition rejections (T1), subscription toggle affecting
available-to-me (T4). M5 addendum: density persisted (T4; placement
decision #6 flagged), flash contract (T3), no autosave (T1). Debug
scaffolding removal → T5. No placeholders; interface gap in
`ingest.Add` explicitly addressed with its migration path (T3).
