# M6a — Frontend Foundation & Read-Only Views Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** The real app renders the M5 winner design read-only: landing
page, the three tab views with full URL-encoded filter/sort state and
HTMX partial swaps, the detail page (including rendered Markdown notes
and cover art served from the data dir) — leaving every mutation (state
changes, notes save, add flow, settings) to M6b.

**Architecture:** `internal/server` grows from the M1 health-check stub
into the full read-only route surface behind a `Deps` struct (mirroring
`ingest.Deps`). Templates are stdlib `html/template` parsed from an
`embed.FS`, one file per page with `{{define}}` blocks for the fragments
HTMX swaps; handlers build typed view models and never pass raw store
rows to templates. Static assets (vendored htmx 2.0.10, `app.css`
derived from the committed M5 winner mock) are embedded and served from
`/assets/`. `internal/store` gains the two query capabilities the M5
addendum added to the contract: sort direction and per-group/state
counts.

**Tech Stack:** Go stdlib `html/template` + `embed` (deliberate,
user-approved deviation from the spec's original templ choice — recorded
in the M5 addendum era decision log below), vendored htmx 2.0.10 (static
file, not a Go dependency), `github.com/yuin/goldmark` for Markdown
rendering (user-approved new dependency).

**Read first:** `docs/superpowers/specs/2026-07-06-media-tracker-design.md`
Section 4 (route/view contracts), and
`docs/superpowers/specs/2026-07-08-m5-ui-addendum.md` + its companion
mock `docs/superpowers/specs/prototypes/2026-07-08-m5-winner.html` —
the mock is the visual target and the source the CSS is extracted from.

## Global Constraints

- Stdlib-first: the only new Go dependency this session is goldmark.
  htmx is vendored as a static file, pinned to 2.0.10.
- No CDN, no network at runtime: all assets served via `embed.FS`.
- Design tokens, class names, and layout come from the M5 winner mock —
  do not invent new visual language; adapt the mock's CSS.
- All filter/sort state URL-encoded → bookmarkable (spec Section 4);
  sort direction is part of that state (M5 addendum change #1).
- User-input errors (bad filter params, unknown item id) are 4xx;
  system failures are 500 and logged (spec Section 5 taxonomy).
  `store.ErrInvalidQuery` → 400; `store.ErrNotFound` → 404.
- Mutations are OUT OF SCOPE (M6b): detail-page action controls render
  (per the mock) but their endpoints don't exist yet. The temporary
  `/debug/*` routes in cmd/mediatracker survive until M6b removes them.
- Tests: `httptest` against the real mux with a seeded real-SQLite temp
  DB (no store mocks); no live APIs anywhere.
- Conventional Commits; no AI attribution anywhere. Errors follow the
  `"pkg: op: %w"` prefix convention. Injected `*slog.Logger` only.

## Design Decisions (flagged for sign-off)

1. **html/template over templ** (user-approved 2026-07-08): stdlib,
   zero codegen; template type safety is replaced by typed view-model
   structs built in handlers, and templates are parsed once at startup
   from `embed.FS` (a parse error fails boot loudly, which is the
   fail-fast we can get without codegen).
2. **goldmark for notes rendering** (user-approved 2026-07-08): default
   configuration — raw HTML in notes is escaped (goldmark's default),
   which is the XSS-safe behavior; no `WithUnsafe`.
3. **HTMX fragment protocol:** handlers check the `HX-Request` header;
   present → execute only the swap-target `{{define}}` block, absent →
   full page. Toolbar controls use `hx-get` + `hx-target` +
   `hx-push-url="true"` so filter state stays bookmarkable.
4. **"Newly available" window** = `now − refresh_interval` (config),
   approximating "since the previous cycle" without new bookkeeping.
   `Deps.RefreshInterval` carries it into the server.
5. **Covers route hardening:** `GET /covers/{name}` serves only names
   matching `^[0-9]+\.jpg$` from `{dataDir}/covers` — no path traversal
   surface. Items without a cover render the CSS monogram placeholder
   (same device as the mock).
6. **Row density** renders from the `row_density` setting (values
   `s|m|l`, default `l` per the M5 addendum) — read-only this session;
   the toggle POST arrives with the settings page in M6b.
7. **Group↔type mapping** lives in one place in `internal/server`:
   `movies-tv → [movie, tv]`, `books → [book]`, `games → [game]`; the
   `type` URL param narrows within the group only for movies-tv, per
   the contract.

## File Structure

```
internal/store/
  query.go              (modify) ListFilter.Dir, dir param, per-sort direction
  query_test.go         (modify)
  counts.go             (new) GroupStateCounts
  counts_test.go        (new)
internal/server/
  server.go             (rework) Deps, New(Deps), route table, healthz kept
  views.go              (new) embed.FS, template parsing, render helpers
  models.go             (new) view-model structs + builders
  handlers.go           (new) landing/tab/detail/covers handlers
  server_test.go        (modify) adapt to Deps
  web_test.go           (new) route-contract tests, seeded temp DB
  assets/htmx.min.js    (new) vendored, pinned 2.0.10
  assets/app.css        (new) extracted from the M5 winner mock
  templates/layout.html (new) top bar, nav, asset includes
  templates/home.html   (new)
  templates/tab.html    (new) incl. {{define "tab-body"}} swap target
  templates/detail.html (new)
cmd/mediatracker/
  main.go               (modify) server.New(Deps{...}); debug routes stay
```

---

### Task 1: Store — Sort Direction & Group/State Counts

**Files:**
- Modify: `internal/store/query.go`, `internal/store/query_test.go`
- Create: `internal/store/counts.go`, `internal/store/counts_test.go`

**Interfaces:**
- Produces: `ListFilter.Dir string` (`""|"asc"|"desc"`; `""` = the
  sort's default: added/year/rating desc, title asc), `dir` URL param in
  `ParseListFilter` (invalid → `ErrInvalidQuery`),
  `(*Store).GroupStateCounts(ctx) (map[MediaType]map[State]int, error)`.
  Tasks 3–4 depend on these exact names.

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/query_test.go`:

```go
func TestParseListFilterDir(t *testing.T) {
	f, err := ParseListFilter(url.Values{"sort": {"year"}, "dir": {"asc"}})
	if err != nil || f.Dir != "asc" {
		t.Fatalf("ParseListFilter dir=asc = (%+v, %v)", f, err)
	}
	if _, err := ParseListFilter(url.Values{"dir": {"sideways"}}); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("invalid dir: err = %v, want ErrInvalidQuery", err)
	}
}

func TestListItemsSortDirection(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedListFixture(t, s)

	items, err := s.ListItems(ctx, ListFilter{Sort: "year", Dir: "asc"})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	got := titles(items)
	want := []string{"Bravo", "Alpha", "Charlie", "Delta"} // 1999,2001,2010,2020
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("year asc: got %v, want %v", got, want)
		}
	}

	items, err = s.ListItems(ctx, ListFilter{Sort: "title", Dir: "desc"})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if got := titles(items); got[0] != "Delta" {
		t.Fatalf("title desc: got %v, want Delta first", got)
	}
}
```

`internal/store/counts_test.go`:

```go
package store

import (
	"context"
	"testing"
)

func TestGroupStateCounts(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedListFixture(t, s) // Alpha movie/want_to, Bravo tv/want_to, Charlie book/in_progress, Delta game/want_to

	counts, err := s.GroupStateCounts(ctx)
	if err != nil {
		t.Fatalf("GroupStateCounts: %v", err)
	}
	if counts[TypeMovie][StateWantTo] != 1 || counts[TypeTV][StateWantTo] != 1 {
		t.Errorf("video counts = %v", counts)
	}
	if counts[TypeBook][StateInProgress] != 1 {
		t.Errorf("book counts = %v", counts)
	}
	if counts[TypeGame][StateWantTo] != 1 {
		t.Errorf("game counts = %v", counts)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ 2>&1 | head -8`
Expected: FAIL — `Dir` field, `dir` handling, `GroupStateCounts` undefined.

- [ ] **Step 3: Implement**

In `internal/store/query.go`: add `Dir string` to `ListFilter` (comment:
`// "" | "asc" | "desc"; "" uses the sort's default direction`). In
`ParseListFilter`, after the sort switch:

```go
	switch dir := v.Get("dir"); dir {
	case "", "asc", "desc":
		f.Dir = dir
	default:
		return ListFilter{}, fmt.Errorf("%w: invalid dir %q", ErrInvalidQuery, dir)
	}
```

In `buildListQuery`, replace the ORDER BY switch so each sort resolves a
default direction that `f.Dir` overrides:

```go
	dir := f.Dir
	if dir == "" {
		if f.Sort == "title" {
			dir = "asc"
		} else {
			dir = "desc"
		}
	}
	up := strings.ToUpper(dir)
	var orderBy string
	switch f.Sort {
	case "", "added":
		orderBy = "mi.added_at " + up + ", mi.id " + up
	case "year":
		orderBy = "mi.release_year " + up + " NULLS LAST, mi.title COLLATE NOCASE ASC"
	case "rating":
		orderBy = "r.avg_score " + up + " NULLS LAST, mi.title COLLATE NOCASE ASC"
	case "title":
		orderBy = "mi.title COLLATE NOCASE " + up
	}
```

(`up` is only ever `"ASC"`/`"DESC"` — validated in `ParseListFilter` and
zero-valued through `ListFilter` literals in Go code, never
user-controlled SQL.)

`internal/store/counts.go`:

```go
package store

import (
	"context"
	"fmt"
)

// GroupStateCounts returns item counts by media type and lifecycle
// state, for the tab badges and the landing page's library panel.
func (s *Store) GroupStateCounts(ctx context.Context) (map[MediaType]map[State]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT media_type, state, COUNT(*) FROM media_items GROUP BY media_type, state`)
	if err != nil {
		return nil, fmt.Errorf("store: group state counts: %w", err)
	}
	defer rows.Close()

	out := make(map[MediaType]map[State]int)
	for rows.Next() {
		var mt MediaType
		var st State
		var n int
		if err := rows.Scan(&mt, &st, &n); err != nil {
			return nil, fmt.Errorf("store: group state counts: %w", err)
		}
		if out[mt] == nil {
			out[mt] = make(map[State]int)
		}
		out[mt][st] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: group state counts: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/store/... -count=1`
Expected: PASS (including all pre-existing sort tests — the default
directions reproduce the old fixed ORDER BY exactly).

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/store/
git commit -m "feat: add sort direction and group/state counts to store"
```

---

### Task 2: Assets, Templates Foundation & Server Rework

**Files:**
- Create: `internal/server/assets/htmx.min.js` (vendored),
  `internal/server/assets/app.css`, `internal/server/templates/layout.html`,
  `internal/server/views.go`, `internal/server/models.go`
- Rework: `internal/server/server.go`, `internal/server/server_test.go`
- Modify: `cmd/mediatracker/main.go`

**Interfaces:**
- Consumes: `store.Store`, `slog.Logger`, `config.RefreshInterval`.
- Produces: `server.Deps{Store *store.Store; Logger *slog.Logger;
  DataDir string; RefreshInterval time.Duration}`,
  `server.New(d Deps) http.Handler`; template render helpers
  `(*views).render(w, name, data)` and `(*views).renderBlock(w, block,
  data)`; routes `GET /assets/{file}` and (kept) `GET /healthz`.
  Tasks 3–5 register their handlers on this foundation.
- Note: `server.New`'s old `(st Store, logger *slog.Logger)` signature
  and its one-method `Store` interface are replaced — `Deps.Store` is
  the concrete `*store.Store` (handlers need many store methods; the
  narrow-interface indirection stops paying its way, and tests already
  use real SQLite). `server_test.go`'s `failingStore` test is replaced
  by an equivalent using a closed real store (call `st.Close()` before
  the request) to keep the 500-path covered without a mock.

- [ ] **Step 1: Vendor htmx (pinned 2.0.10)**

```bash
mkdir -p internal/server/assets
curl -fsSL -o internal/server/assets/htmx.min.js \
  https://cdn.jsdelivr.net/npm/htmx.org@2.0.10/dist/htmx.min.js
head -c 100 internal/server/assets/htmx.min.js   # sanity: JS, not an error page
grep -c "htmx" internal/server/assets/htmx.min.js # non-zero
```

Record the file's `sha256sum` output in the commit message body.

- [ ] **Step 2: Extract app.css from the winner mock**

Source: `docs/superpowers/specs/prototypes/2026-07-08-m5-winner.html`
(in-repo). Copy the entire `<style>` block into
`internal/server/assets/app.css`, then apply exactly these deletions —
they are prototype-only scaffolding:

- the `.viewnote` rule (prototype caption)
- the `.search-pop .hint` rule (mock explainer text)
- the `.sizes` rules STAY (density toggle renders read-only this
  session; M6b wires the POST)

Everything else — tokens (both themes + explicit `data-theme`
overrides), top bar, toolbar, table, landing panels, type tints, state
chips, detail (`.detail`, `.bigcover`, `.rsource`, `.avchip`,
`.metagrid`, notes styles) — is kept verbatim, class names unchanged.
Add one rule at the end for cover images (the mock only had CSS
placeholder covers):

```css
.thumb img,.bigcover img{width:100%;height:100%;object-fit:cover;border-radius:inherit;display:block}
```

- [ ] **Step 3: Write the failing tests**

Rewrite `internal/server/server_test.go`:

```go
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/varigg/mediatracker/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *store.Store, string) {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dataDir, "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv := httptest.NewServer(New(Deps{
		Store:           st,
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, nil)),
		DataDir:         dataDir,
		RefreshInterval: 7 * 24 * time.Hour,
	}))
	t.Cleanup(srv.Close)
	return srv, st, dataDir
}

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	if _, err := io.Copy(&b, resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, b.String()
}

func TestHealthzOK(t *testing.T) {
	srv, _, _ := newTestServer(t)
	resp, body := get(t, srv, "/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(body), &m); err != nil || m["status"] != "ok" {
		t.Errorf("body = %q", body)
	}
}

func TestHealthzFailsLoudly(t *testing.T) {
	srv, st, _ := newTestServer(t)
	st.Close() // simulate a dead database
	resp, _ := get(t, srv, "/healthz")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestAssetsServed(t *testing.T) {
	srv, _, _ := newTestServer(t)
	for path, needle := range map[string]string{
		"/assets/htmx.min.js": "htmx",
		"/assets/app.css":     "--accent",
	} {
		resp, body := get(t, srv, path)
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, needle) {
			t.Errorf("%s: status %d, contains(%q)=%v", path, resp.StatusCode, needle, strings.Contains(body, needle))
		}
	}
}
```

(add `"io"` to imports.)

- [ ] **Step 4: Run tests to verify they fail**

Run: `go test ./internal/server/ 2>&1 | head -8`
Expected: FAIL — `Deps`/`New(Deps)` undefined.

- [ ] **Step 5: Implement the foundation**

`internal/server/server.go` (full replacement):

```go
// Package server is the HTTP layer: the full route surface rendering
// the M5 winner design via html/template + HTMX.
package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/varigg/mediatracker/internal/store"
)

// Deps wires everything the HTTP layer needs.
type Deps struct {
	Store           *store.Store
	Logger          *slog.Logger
	DataDir         string        // covers are served from {DataDir}/covers
	RefreshInterval time.Duration // bounds the "newly available" window
}

func New(d Deps) http.Handler {
	v := newViews()
	s := &site{deps: d, views: v}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assetsFS())))
	// Read-only views (this session); mutations land in M6b.
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /movies-tv", s.tab("movies-tv"))
	mux.HandleFunc("GET /books", s.tab("books"))
	mux.HandleFunc("GET /games", s.tab("games"))
	mux.HandleFunc("GET /items/{id}", s.detail)
	mux.HandleFunc("GET /covers/{name}", s.cover)
	return mux
}

type site struct {
	deps  Deps
	views *views
}
```

`internal/server/views.go`:

```go
package server

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed assets/*
var rawAssetsFS embed.FS

func assetsFS() fs.FS {
	sub, err := fs.Sub(rawAssetsFS, "assets")
	if err != nil {
		panic(err) // embedded path is a compile-time fact; boot-time failure is correct
	}
	return sub
}

// views holds the parsed template set. Every page template is parsed
// together with layout.html so pages fill layout's {{block}} slots.
type views struct {
	pages map[string]*template.Template
}

func newViews() *views {
	pages := map[string]*template.Template{}
	for _, page := range []string{"home.html", "tab.html", "detail.html"} {
		t := template.Must(template.ParseFS(templatesFS,
			"templates/layout.html", "templates/"+page))
		pages[page] = t
	}
	return &views{pages: pages}
}

// render writes the full page (layout + page).
func (v *views) render(w http.ResponseWriter, page string, data any) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t, ok := v.pages[page]
	if !ok {
		return fmt.Errorf("server: unknown page %q", page)
	}
	return t.ExecuteTemplate(w, "layout", data)
}

// renderBlock writes one named {{define}} block from a page — the HTMX
// fragment path.
func (v *views) renderBlock(w http.ResponseWriter, page, block string, data any) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t, ok := v.pages[page]
	if !ok {
		return fmt.Errorf("server: unknown page %q", page)
	}
	return t.ExecuteTemplate(w, block, data)
}
```

`internal/server/models.go` — start with the layout-level model plus the
healthz handler's move; page models grow in Tasks 3–5:

```go
package server

import (
	"encoding/json"
	"net/http"

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
```

`internal/server/templates/layout.html` — structure mirrors the mock's
top bar (wordmark, Home + three group tabs with type dots and counts,
quick-search placeholder input — non-functional until M6b's search
partial):

```html
{{define "layout"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>mediatracker</title>
<link rel="stylesheet" href="/assets/app.css">
<script src="/assets/htmx.min.js" defer></script>
</head>
<body>
<header class="top"><div class="top-in">
  <a class="wordmark" href="/">media<b>tracker</b></a>
  <nav class="tabs">
    <a href="/" class="{{if eq .Nav.Active ""}}on{{end}}">Home</a>
    <a href="/movies-tv" class="{{if eq .Nav.Active "movies-tv"}}on{{end}}"><span class="tdot video"></span>Movies &amp; TV <span class="n mono">{{index .Nav.Counts "movies-tv"}}</span></a>
    <a href="/books" class="{{if eq .Nav.Active "books"}}on{{end}}"><span class="tdot book"></span>Books <span class="n mono">{{index .Nav.Counts "books"}}</span></a>
    <a href="/games" class="{{if eq .Nav.Active "games"}}on{{end}}"><span class="tdot game"></span>Games <span class="n mono">{{index .Nav.Counts "games"}}</span></a>
  </nav>
  <div class="searchbox">
    <span class="glyph">⌕</span>
    <input placeholder="Add a movie, book, game…" disabled title="Add flow arrives in the next milestone">
  </div>
</div></header>
<main class="wrap">{{block "content" .}}{{end}}</main>
</body>
</html>{{end}}
```

(The mock styles `nav.tabs button`; app.css needs those selectors to
also match `nav.tabs a` — adjust the extracted CSS: change the
`nav.tabs button` selector group to `nav.tabs :is(a,button)`, keeping
everything else identical. Same for `.wordmark` now being an `<a>`.)

Update `cmd/mediatracker/main.go`: replace `server.New(st, logger)` with

```go
	mux.Handle("/", server.New(server.Deps{
		Store:           st,
		Logger:          logger,
		DataDir:         *dataDir,
		RefreshInterval: cfg.RefreshInterval.Duration,
	}))
```

The `/debug/*` registrations stay untouched. Note the route overlap:
`registerDebugRoutes` owns `/debug/*`; everything else falls through to
`server.New`'s mux, exactly as before.

Until Tasks 3–5 land their handlers, stub `home`, `tab`, `detail`, and
`cover` in `handlers.go` with `http.Error(w, "not implemented",
http.StatusNotImplemented)` bodies so this task compiles and its tests
pass on its own — each following task replaces its stub with the real
handler and its own tests.

- [ ] **Step 6: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/server/... -count=1 && go build ./...`
Expected: PASS, clean build.

- [ ] **Step 7: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/server/ cmd/mediatracker/
git commit -m "feat: add server foundation with embedded assets and template infrastructure"
```

(Include the htmx sha256 in this commit's body.)

---

### Task 3: Landing Page

**Files:**
- Create: `internal/server/templates/home.html`
- Modify: `internal/server/handlers.go` (real `home`),
  `internal/server/models.go` (home view model)
- Create: `internal/server/web_test.go` (shared seed helper + home tests)

**Interfaces:**
- Consumes: `store.ListItems(ListFilter{State: StateInProgress})`,
  `store.NewlyAvailable(since)`, `s.nav`, Task 2's render helpers.
- Produces: `GET /` rendering: *Continue* and *Newly available* panels
  grouped by media group with type-tinted rows and dot subheaders,
  *Library* panel with per-group want-to/in-progress/done counts.
  Exact markup mirrors the mock's `renderHome` output: panels of
  `.ph` subheaders + `.lrow` rows, `.libline` library lines — reuse the
  mock's class names so the extracted CSS applies unchanged.

- [ ] **Step 1: Write the failing tests**

`internal/server/web_test.go` — one shared seed used by Tasks 3–5:

```go
package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/varigg/mediatracker/internal/store"
)

// seedWeb populates a store with one item per interesting situation:
// an in-progress TV show, a want-to movie newly available on a
// subscribed service, a done book, and a want-to game with ownership.
func seedWeb(t *testing.T, st *store.Store) (ids map[string]int64) {
	t.Helper()
	ctx := context.Background()
	ids = map[string]int64{}
	mk := func(key string, n store.NewItem) *store.MediaItem {
		it, _, err := st.CreateItem(ctx, n)
		if err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
		ids[key] = it.ID
		return it
	}
	tv := mk("tv", store.NewItem{MediaType: store.TypeTV, Title: "Severance",
		ReleaseYear: intp(2022), Genres: []string{"Drama"}, Provider: "tmdb", ProviderID: "tv:1"})
	if err := st.UpdateState(ctx, tv.ID, store.StateInProgress); err != nil {
		t.Fatal(err)
	}
	movie := mk("movie", store.NewItem{MediaType: store.TypeMovie, Title: "Dune: Part Two",
		ReleaseYear: intp(2024), Genres: []string{"Sci-Fi"}, Provider: "tmdb", ProviderID: "movie:2"})
	if err := st.SetServiceSubscribed(ctx, "netflix", true); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAvailability(ctx, movie.ID, []store.Availability{
		{ServiceSlug: "netflix", Kind: store.KindSubscription}}); err != nil {
		t.Fatal(err)
	}
	book := mk("book", store.NewItem{MediaType: store.TypeBook, Title: "The Hobbit",
		ReleaseYear: intp(1937), Genres: []string{"Fantasy"}, Provider: "openlibrary", ProviderID: "OL1"})
	if err := st.UpdateState(ctx, book.ID, store.StateDone); err != nil {
		t.Fatal(err)
	}
	game := mk("game", store.NewItem{MediaType: store.TypeGame, Title: "Hades",
		ReleaseYear: intp(2020), Genres: []string{"Roguelike"}, Provider: "igdb", ProviderID: "g1"})
	if err := st.ReplaceRatings(ctx, game.ID, []store.Rating{
		{Source: "igdb", Score: 93, Display: "93/100"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAvailability(ctx, game.ID, []store.Availability{
		{ServiceSlug: "steam", Kind: store.KindOwned}}); err != nil {
		t.Fatal(err)
	}
	return ids
}

func intp(i int) *int { return &i }

func TestHomeRendersSections(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	resp, body := get(t, srv, "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, needle := range []string{
		"Continue",           // section
		"Severance",          // the in-progress item
		"Newly available",    // section
		"Dune: Part Two",     // newly available on subscribed netflix
		"Library",            // counts panel
		`class="lrow video"`, // type-tinted row
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("home missing %q", needle)
		}
	}
	if strings.Contains(body, "The Hobbit") {
		t.Error("done book must not appear in Continue/Newly available")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/server/ -run TestHome 2>&1 | head -6`
Expected: FAIL — 501 from the stub.

- [ ] **Step 3: Implement**

Add to `internal/server/models.go`:

```go
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
	Group, Label, DotClass       string
	WantTo, InProgress, DoneN    int
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
```

(add `"strings"` import to models.go.)

`internal/server/handlers.go` — replace the `home` stub:

```go
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
		Nav:      nav,
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
```

`internal/server/templates/home.html` — mirror the mock's home view
(two-column `homegrid`, `.ph` grouped panels, `.libline` library):

```html
{{define "content"}}
<div class="homegrid">
 <div>
  <h2 class="sec">Continue</h2>
  <div class="panel">
   {{range .Continue}}
    <div class="ph"><span class="tdot {{.DotClass}}"></span>{{.Label}}<span class="mono">{{len .Rows}}</span></div>
    {{range .Rows}}
    <a class="lrow {{.DotClass}}" href="/items/{{.ID}}">
      {{template "thumb" .Cover}}
      <span><span class="lt">{{.Title}}</span><br><span class="ls">{{.Sub}}</span></span>
      <span class="right"><span class="state in_progress">{{.Right}}</span></span></a>
    {{end}}
   {{end}}
  </div>
 </div>
 <div>
  <h2 class="sec">Newly available on your services</h2>
  <div class="panel">
   {{range .Newly}}
    <div class="ph"><span class="tdot {{.DotClass}}"></span>{{.Label}}<span class="mono">{{len .Rows}}</span></div>
    {{range .Rows}}
    <a class="lrow {{.DotClass}}" href="/items/{{.ID}}">
      {{template "thumb" .Cover}}
      <span><span class="lt">{{.Title}}</span><br><span class="ls">{{.Sub}}</span></span>
      <span class="right mono">{{.Right}}</span></a>
    {{end}}
   {{end}}
  </div>
 </div>
</div>
<h2 class="sec">Library</h2>
<div class="panel">
 {{range .Library}}
 <a class="libline {{.DotClass}}" href="/{{.Group}}">
   <span class="g" style="display:flex;gap:8px;align-items:center"><span class="tdot {{.DotClass}}"></span>{{.Label}}</span>
   <span class="mono">{{.WantTo}} want to</span><span class="mono">{{.InProgress}} in progress</span>
   <span class="mono">{{.DoneN}} done</span><span class="mono" style="margin-left:auto">→</span></a>
 {{end}}
</div>
{{end}}

{{define "thumb"}}{{if .URL}}<span class="thumb"><img src="{{.URL}}" alt=""></span>{{else}}<span class="thumb" style="background-color:hsl({{.Hue}} 32% 40%)">{{.Monogram}}</span>{{end}}{{end}}
```

Note: `.lrow`/`.libline` were `<button>`s in the mock; as real
navigation they're `<a>`s — extend the extracted CSS selectors the same
way as the nav tabs (`.lrow` and `.libline` rules already target
classes, so only check `text-align`/`display` still apply to anchors;
add `a.lrow,a.libline{color:inherit}` if needed).

- [ ] **Step 4: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/server/... -count=1`
Expected: PASS

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/server/
git commit -m "feat: render the landing page"
```

---

### Task 4: Tab Views with URL State & HTMX Swaps

**Files:**
- Create: `internal/server/templates/tab.html`
- Modify: `internal/server/handlers.go` (real `tab`),
  `internal/server/models.go` (tab view model), `internal/server/web_test.go`

**Interfaces:**
- Consumes: `store.ParseListFilter`, `store.ListItems`,
  `store.GroupStateCounts`, `store.GetSetting("row_density")`.
- Produces: `GET /{group}?state=&type=&genre=&available=&sort=&dir=`
  rendering the ledger table; the `{{define "tab-body"}}` block is the
  HTMX swap target (toolbar + table + result meta all inside it, since
  filters change all three); toolbar controls emit
  `hx-get`/`hx-target="#tab-body"`/`hx-push-url="true"` requests and the
  handler returns only the block when `HX-Request: true`.
- Contract details: `state` defaults to `want_to` (spec); the group
  pre-filters types and, for movies-tv only, a `type` param narrows to
  movie or tv; invalid params → 400 (`ErrInvalidQuery`); sorting by
  added/year/rating/title with `dir` (M5 addendum); density from the
  `row_density` setting (`s|m|l`, default `l`) rendered as a class
  `density-s|m|l` on the table wrapper — add three CSS rules to app.css
  overriding `--thumb-h` (38/56/84px) per class.

- [ ] **Step 1: Write the failing tests**

Append to `internal/server/web_test.go`:

```go
func TestTabDefaultsToWantTo(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	resp, body := get(t, srv, "/movies-tv")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "Dune: Part Two") { // want_to movie
		t.Error("want_to movie missing from default tab view")
	}
	if strings.Contains(body, "Severance") { // in_progress: filtered out by default state
		t.Error("in_progress item leaked into want_to view")
	}
}

func TestTabStateAndTypeFilters(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	_, body := get(t, srv, "/movies-tv?state=in_progress&type=tv")
	if !strings.Contains(body, "Severance") {
		t.Error("tv in_progress filter missed Severance")
	}
	_, body = get(t, srv, "/books?state=done")
	if !strings.Contains(body, "The Hobbit") {
		t.Error("done book missing")
	}
}

func TestTabAvailableToMe(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	_, body := get(t, srv, "/games?available=1")
	if !strings.Contains(body, "Hades") { // steam-owned counts as available
		t.Error("owned game missing from available-to-me")
	}
}

func TestTabInvalidParamsAre400(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	for _, q := range []string{"?state=pending", "?sort=popularity", "?dir=sideways"} {
		resp, _ := get(t, srv, "/games"+q)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, resp.StatusCode)
		}
	}
}

func TestTabHTMXFragment(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	req, _ := http.NewRequest("GET", srv.URL+"/games", nil)
	req.Header.Set("HX-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if strings.Contains(body, "<!doctype html>") {
		t.Error("HX-Request must return the fragment, not the full page")
	}
	if !strings.Contains(body, "Hades") {
		t.Error("fragment missing table content")
	}
}
```

(add `"io"` to web_test.go imports.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/server/ -run TestTab 2>&1 | head -8`
Expected: FAIL — 501 from the stub.

- [ ] **Step 3: Implement**

Handler (`tab` returns a closure per group; full page vs block chosen by
`HX-Request`):

```go
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
```

View model + builder in models.go:

```go
type TabRow struct {
	ID          int64
	Title       string
	Genres      string
	Type        store.MediaType
	Year        *int
	Rating      *int // avg across sources, nil when unrated
	Avail       []AvailBadge
	State       store.State
	StateLabel  string
	Added       string
	DotClass    string
	Cover       *CoverRef
}

type AvailBadge struct {
	Label string
	Class string // "sub" | "own" | ""
}

type StateTab struct {
	State  store.State
	Label  string
	Count  int
	Active bool
}

type TabData struct {
	Nav        Nav
	Group      string
	Label      string
	Sub        string // "" | "movie" | "tv"
	States     []StateTab
	Filter     store.ListFilter
	Rows       []TabRow
	Total      int
	Density    string // s|m|l
	Query      template.URL // current query string minus state, for state links
}
```

The builder (`tabData`) fetches `GroupStateCounts` for the state-tab
counts (summed across the group's types, respecting `sub` when set),
`GetSetting("row_density")` (default `"l"`), ratings/availability per
row via `GetRatings`/`GetAvailability` plus a `ListServices`-derived
subscribed map (one call, not per row) to classify badges
(`owned→"own"`, `subscribed→"sub"`, else `""`). `stateNames` map:
want_to "Want to", in_progress "In progress", done "Done", abandoned
"Abandoned". Rating = mean of rating scores rounded, nil if none.

`templates/tab.html` — everything inside `{{define "tab-body"}}` so one
swap updates toolbar (active states/counts), table, and meta line;
`{{define "content"}}` just wraps the body in `<div id="tab-body">`:

```html
{{define "content"}}<div id="tab-body">{{template "tab-body" .}}</div>{{end}}

{{define "tab-body"}}
<div class="toolbar">
  <div class="states">
    {{$d := .}}
    {{range .States}}
    <a class="{{if .Active}}on{{end}}"
       hx-get="/{{$d.Group}}?state={{.State}}{{if $d.Sub}}&type={{$d.Sub}}{{end}}"
       hx-target="#tab-body" hx-push-url="true"
       href="/{{$d.Group}}?state={{.State}}">{{.Label}}<span class="mono">{{.Count}}</span></a>
    {{end}}
  </div>
  {{if eq .Group "movies-tv"}}
  <div class="subtabs">
    <a class="{{if eq .Sub ""}}on{{end}}" hx-get="/movies-tv?state={{.Filter.State}}" hx-target="#tab-body" hx-push-url="true" href="/movies-tv?state={{.Filter.State}}">All</a>
    <a class="{{if eq .Sub "movie"}}on{{end}}" hx-get="/movies-tv?state={{.Filter.State}}&type=movie" hx-target="#tab-body" hx-push-url="true" href="/movies-tv?state={{.Filter.State}}&type=movie">Movies</a>
    <a class="{{if eq .Sub "tv"}}on{{end}}" hx-get="/movies-tv?state={{.Filter.State}}&type=tv" hx-target="#tab-body" hx-push-url="true" href="/movies-tv?state={{.Filter.State}}&type=tv">TV</a>
  </div>
  {{end}}
  <div class="tool-right">
    <label class="avail"><input type="checkbox" {{if .Filter.Available}}checked{{end}}
      hx-get="/{{.Group}}?state={{.Filter.State}}{{if .Sub}}&type={{.Sub}}{{end}}{{if not .Filter.Available}}&available=1{{end}}"
      hx-target="#tab-body" hx-push-url="true">Available to me</label>
  </div>
</div>
<div class="tablewrap density-{{.Density}}"><table>
  <thead><tr>
    <th></th>
    {{template "th" args . "title" "Title"}}
    <th>Type</th>
    {{template "th" args . "year" "Year"}}
    {{template "th" args . "rating" "Rating"}}
    <th>Availability</th><th>State</th>
    {{template "th" args . "added" "Added"}}
  </tr></thead>
  <tbody>
  {{range .Rows}}
  <tr class="{{.DotClass}}" onclick="window.location='/items/{{.ID}}'">
    <td style="width:38px">{{template "thumb" .Cover}}</td>
    <td class="titlecell">{{.Title}}<span class="sub">{{.Genres}}</span></td>
    <td><span class="typechip {{.Type}}">{{.Type}}</span></td>
    <td class="mono">{{if .Year}}{{.Year}}{{end}}</td>
    <td class="mono">{{if .Rating}}★ {{.Rating}}{{end}}</td>
    <td>{{range .Avail}}<span class="badge {{.Class}}">{{.Label}}</span> {{end}}</td>
    <td><span class="state {{.State}}">{{.StateLabel}}</span></td>
    <td class="mono" style="color:var(--muted)">{{.Added}}</td>
  </tr>
  {{else}}
  <tr><td colspan="8" class="empty">Nothing here — try another filter.</td></tr>
  {{end}}
  </tbody>
</table></div>
<p class="resultmeta mono">{{.Total}} items</p>
{{end}}
```

Column-header sort links need current-vs-clicked sort logic; implement
`th` as a template invoked with an `args` FuncMap helper
(`func(d TabData, sort, label string) map[string]any`) that computes
the target URL (same state/type/available, `sort=X`, `dir` toggled if
already active) and the direction glyph. Register `args` in `newViews`
via `template.FuncMap` before parsing. Keep it a pure function in
views.go with its own unit-testable core:
`sortLink(d TabData, sort string) (href string, glyph string)`.

Row-click navigation uses a plain `onclick` (as the mock did); the
title cell also gets a real `<a>` for keyboard/a11y:
wrap `{{.Title}}` in `<a href="/items/{{.ID}}">`.

app.css additions (with the extraction, or now):

```css
.tablewrap.density-s{--thumb-h:38px}
.tablewrap.density-m{--thumb-h:56px}
.tablewrap.density-l{--thumb-h:84px}
```

and remove the `.sizes` toggle from being rendered anywhere this
session (CSS may stay).

- [ ] **Step 4: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/server/... -count=1`
Expected: PASS

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/server/
git commit -m "feat: render tab views with URL filter state and HTMX swaps"
```

---

### Task 5: Detail Page & Covers Route

**Files:**
- Create: `internal/server/templates/detail.html`
- Modify: `internal/server/handlers.go` (real `detail`, `cover`),
  `internal/server/models.go`, `internal/server/web_test.go`
- Modify: `go.mod`/`go.sum` (goldmark)

**Interfaces:**
- Consumes: `store.GetItem` (404 on `ErrNotFound`), `GetRatings`,
  `GetAvailability`, `ListServices`, `store.LegalTransitions`,
  goldmark for notes.
- Produces: `GET /items/{id}` per the M5 winner detail (large sticky
  cover, Status/Verdict/Ratings/Availability/Notes/Details sections);
  action controls render but are non-functional this session (buttons
  carry `disabled` + `title="Interactive in the next milestone"` so the
  checkpoint is honest); notes render through goldmark (safe defaults).
  `GET /covers/{name}`: serves `{dataDir}/covers/{name}` for names
  matching `^[0-9]+\.jpg$`, 404 otherwise or when absent.

- [ ] **Step 1: Add goldmark**

```bash
go get github.com/yuin/goldmark@latest
```

- [ ] **Step 2: Write the failing tests**

Append to `internal/server/web_test.go`:

```go
func TestDetailRenders(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	ctx := context.Background()
	if err := st.UpdateNotes(ctx, ids["game"], "Heat 16. **Coach Skelly** believes."); err != nil {
		t.Fatal(err)
	}
	resp, body := get(t, srv, fmt.Sprintf("/items/%d", ids["game"]))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, needle := range []string{
		"Hades",
		"93/100",                          // rating display string
		"steam",                           // availability badge
		"<strong>Coach Skelly</strong>",   // markdown rendered
		"Start",                           // legal transition from want_to
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("detail missing %q", needle)
		}
	}
	if strings.Contains(body, "Re-watch") { // done-only transition must not render for want_to
		t.Error("illegal transition rendered")
	}
}

func TestDetailNotFound(t *testing.T) {
	srv, st, _ := newTestServer(t)
	seedWeb(t, st)
	resp, _ := get(t, srv, "/items/99999")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestNotesEscapeRawHTML(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ids := seedWeb(t, st)
	if err := st.UpdateNotes(context.Background(), ids["game"], `<script>alert(1)</script>`); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, srv, fmt.Sprintf("/items/%d", ids["game"]))
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("raw HTML in notes must be escaped")
	}
}

func TestCoversServedAndHardened(t *testing.T) {
	srv, st, dataDir := newTestServer(t)
	ids := seedWeb(t, st)
	dir := filepath.Join(dataDir, "covers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("%d.jpg", ids["game"])
	if err := os.WriteFile(filepath.Join(dir, name), []byte("\xff\xd8fakejpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, _ := get(t, srv, "/covers/"+name)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("cover: status = %d", resp.StatusCode)
	}
	for _, bad := range []string{"/covers/../app.db", "/covers/evil.txt", "/covers/1.png"} {
		resp, _ := get(t, srv, bad)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s must not be served", bad)
		}
	}
}
```

(add `"fmt"`, `"os"`, `"path/filepath"` to imports.)

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/server/ -run 'TestDetail|TestNotes|TestCovers' 2>&1 | head -8`
Expected: FAIL — 501 stubs.

- [ ] **Step 4: Implement**

`cover` handler:

```go
var coverName = regexp.MustCompile(`^[0-9]+\.jpg$`)

func (s *site) cover(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !coverName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.deps.DataDir, "covers", name))
}
```

`detail` handler: `strconv.ParseInt(r.PathValue("id"))` (bad → 400),
`GetItem` (`errors.Is(err, store.ErrNotFound)` → 404, else 500), fetch
ratings + availability + subscribed map, build:

```go
type DetailData struct {
	Nav         Nav
	Item        *store.MediaItem
	Group       string
	GroupLabel  string
	TypeChip    string
	StateLabel  string
	Genres      string
	Cover       *CoverRef
	Transitions []TransitionButton
	Terminal    bool
	Ratings     []RatingCard
	Avail       []AvailChip
	NotesHTML   template.HTML // goldmark output of item.Notes
	VerbFor     string        // watch | read | play
}

type TransitionButton struct{ To store.State; Label string; Primary bool }
type RatingCard struct{ Display, Source string; URL *string }
type AvailChip struct{ Label, Kind, Class string; URL *string }
```

Transition labels reproduce the mock/M4 semantics:
`want_to→"Move to Want to"`, `in_progress→"Start"` (or
`"Re-watch / resume"` when coming from done), `done→"Mark done"`,
`abandoned→"Abandon"`; order = `store.LegalTransitions(state)`, first
is Primary. Markdown:

```go
func renderMarkdown(src string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := goldmark.New().Convert([]byte(src), &buf); err != nil {
		return "", fmt.Errorf("server: render notes: %w", err)
	}
	return template.HTML(buf.String()), nil
}
```

(goldmark's default escapes raw HTML — the XSS test pins that.)

`templates/detail.html` mirrors the mock's A-style detail: `.detail`
grid, `.dcoverwrap`+`.bigcover` (img or monogram via the shared
`thumb`-like pattern — add a `bigcover` variant of the thumb define
taking the same `CoverRef`), `.dtitle`, `.dsub` with typechip + year +
genres + state chip + completed date, then sections: Status (transition
`<button disabled title="Interactive in the next milestone">`), Verdict
(only when Terminal; three disabled pills, `.on` for current), Ratings
(`.rsource` cards, link-out when URL present), Availability (`.avchip`
with sub/own classes; owned chips link to their store URL when present),
Notes (`<div class="nprev">{{.NotesHTML}}</div>` — read-only display;
the editor arrives in M6b), Details (`.metagrid`: provider, provider id,
added, refreshed).

- [ ] **Step 5: Run tests to verify they pass**

Run: `set -o pipefail; go test ./internal/server/... -count=1`
Expected: PASS

- [ ] **Step 6: Format, vet, commit**

```bash
gofmt -l . && go vet ./... && set -o pipefail && go test ./... -count=1
git add internal/server/ go.mod go.sum
git commit -m "feat: render detail page with markdown notes and serve covers"
```

---

### Task 6: Integration Sweep & Boot Smoke

**Files:** none new — verification task.

- [ ] **Step 1: Full suite + coverage floors (CI parity)**

```bash
gofmt -l . && go vet ./... && go build ./...
go test ./... -race -count=1 -coverprofile=coverage.out
go run github.com/mach6/go-covercheck/cmd/go-covercheck@v0.6.1 --no-color coverage.out
```

Expected: all green. If a floor trips, the gap is genuinely-new
untested code from this session — fix the test gap, don't lower floors.

- [ ] **Step 2: Boot smoke test — browse the real app**

```bash
SMOKE_DIR=$(mktemp -d)
go build -o /tmp/mediatracker-smoke ./cmd/mediatracker
printf 'listen_addr = ":18090"\n' > "$SMOKE_DIR/config.toml"
/tmp/mediatracker-smoke -data "$SMOKE_DIR" &
sleep 1
curl -s http://localhost:18090/ | grep -c "mediatracker"      # landing renders
curl -s http://localhost:18090/games | grep -c "tablewrap"    # tab renders
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:18090/items/1   # 404 (empty db)
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:18090/assets/app.css # 200
kill %1
```

- [ ] **Step 3: Commit any stragglers, update ledger**

---

## Deferred to M6b (explicitly out of scope here)

Mutations (`POST /items/{id}/state`, `/review`, `PUT notes` + preview
partial, `POST /items/{id}/refresh`, `POST /refresh`), the add flow
(`GET /search` partial + `POST /items` with 10s budget, duplicate-add
flash), the settings page (+ density toggle POST, subscription toggles),
deleting `cmd/mediatracker`'s `/debug/*` routes, enabling the top-bar
search box, and phone-layout polish.

## Self-Review Notes

Spec §4 read-only coverage: landing → Task 3; tab views w/ URL state →
Task 4; detail (all display contracts incl. subscribed highlight, owned
store links, legal-transitions-only rendering, Markdown notes display)
→ Task 5; covers from data dir → Task 5; embedded assets → Task 2.
M5 addendum changes honored: dir param (Task 1/4), type-grouped landing
(Task 3), density from settings read-only (Task 4), table-not-cards
(Task 4). Error taxonomy: 400s via ErrInvalidQuery (Task 4 test), 404
via ErrNotFound (Task 5 test), 500 path via closed-store healthz test
(Task 2). No placeholders: complete code or exact in-repo extraction
instructions (the CSS from the committed winner mock) in every step.
