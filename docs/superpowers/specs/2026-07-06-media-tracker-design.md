# Media Tracker — Design

Status: all sections approved; self-reviewed; awaiting final user review
Date: 2026-07-06

## Overview

Self-hosted, single-user web app for tracking movies, TV shows, books, and
games across a want-to → in-progress → done/abandoned lifecycle, with
personal reviews and notes, externally sourced metadata and cover art,
streaming/subscription availability, and game-ownership links.

## Decision Ledger

- **Form**: Self-hosted web app, single-user, no auth; LAN/Tailscale is the
  security boundary. Bare Go binary + systemd; SQLite; data dir on disk.
- **Stack**: Go + SQLite + HTMX/templ, server-rendered. One binary, no Node.
- **Media types**: Movies, TV (whole-series granularity), Books, Games —
  unified lifecycle across all four.
- **States**: `want_to → in_progress → done`, plus `abandoned`. Simple
  mutable state, one row per title; notes accrete across the lifecycle
  (no consumption-history table).
- **Reviews**: liked/ok/disliked verdict, completion date, Markdown notes.
- **Sources (official APIs)**: TMDB (movie/TV metadata, covers, streaming
  availability via its JustWatch-backed provider data), OMDb (IMDB + Rotten
  Tomatoes scores), Open Library + Hardcover composed for books, IGDB for
  games, Steam Web API for ownership. **One sanctioned exception**: Game
  Pass / PS+ catalogs via unofficial endpoints, quarantined behind adapters.
- **Availability**: US region; show all services, highlight subscribed ones.
  Subscriptions configured on a settings page (checklist). Owned games link
  to their web store/library page.
- **Freshness**: Periodic background refresh (~weekly) for want-to and
  in-progress items; done/abandoned items frozen.
- **UX**: Three tabs (Movies/TV, Books, Games); state filters within each;
  sort by attributes; filter by availability-on-my-services and genre/tags;
  in-app search to add items.

## Architecture Choice

**Approach A — Unified item + typed adapters (chosen).** One `media_items`
table carrying universal columns plus promoted sortable/filterable columns,
with type-specific residue in a JSON `metadata` column. Each media type gets
a `MetadataProvider` implementation behind one interface. Availability and
ownership live in separate tables with their own refresh cadence.

Rejected alternatives:

- **B — Vertical slices per media type**: fully typed per-type tables and
  handlers. Maximum type safety, but quadruplicates lifecycle/review/refresh
  machinery; cross-type views require UNIONs. Sustained tax at solo scale.
- **C — Unified core + per-type extension tables**: relationally pure, no
  JSON, but every hydration is a join and every new metadata field is a
  migration. Rigor that pays at team scale, not for a personal tool.

## Section 1 — Data Model (SQLite) [APPROVED]

Five tables. `media_items` is the spine:

```sql
CREATE TABLE media_items (
    id            INTEGER PRIMARY KEY,
    media_type    TEXT NOT NULL CHECK (media_type IN ('movie','tv','book','game')),
    title         TEXT NOT NULL,
    state         TEXT NOT NULL DEFAULT 'want_to'
                  CHECK (state IN ('want_to','in_progress','done','abandoned')),
    verdict       TEXT CHECK (verdict IN ('liked','ok','disliked')),
    completed_at  DATE,              -- set on done/abandoned
    notes         TEXT NOT NULL DEFAULT '',   -- Markdown, lifecycle-long
    release_year  INTEGER,
    genres        TEXT NOT NULL DEFAULT '[]', -- JSON array, promoted for filtering
    cover_path    TEXT,              -- local file under data-dir/covers/
    provider      TEXT NOT NULL,     -- 'tmdb' | 'openlibrary' | 'igdb'
    provider_id   TEXT NOT NULL,     -- canonical ID in that provider
    metadata      TEXT NOT NULL DEFAULT '{}', -- type-specific JSON residue
    added_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    refreshed_at  DATETIME,
    UNIQUE (provider, provider_id)
);
```

- **`ratings(item_id, source, score, display, url)`** — one row per rating
  source (`imdb`, `rotten_tomatoes`, `hardcover`, `igdb`, `metacritic`, …).
  `score` normalized to 0–100 at ingest for coherent cross-source sorting;
  `display` preserves the original scale for rendering (e.g. `7.9/10`).
- **`availability(item_id, service_slug, kind, url, first_seen_at,
  fetched_at)`** — `kind` is `stream` | `subscription` | `owned`. Separate
  from items because it has its own refresh cadence and provenance. A
  Steam-owned game is one row (`kind='owned'`, url = store page); Netflix
  availability is another. Upserts bump `fetched_at` but preserve
  `first_seen_at`, which powers the landing page's "newly available" diff.
  "Highlight mine" is a join against `services`, not a column.
- **`services(slug, name, media_kind, subscribed)`** — seeded with the US
  catalog; `subscribed` backs the settings-page checklist.
- **`settings(key, value)`** — region, refresh interval, Steam ID, etc.

Notes:

- `UNIQUE (provider, provider_id)` makes add-item idempotent; re-adding
  surfaces the existing item.
- Verdict/completed_at are nullable and only meaningful in terminal states;
  state-transition legality is enforced in Go, not CHECK constraints.

## Section 2 — Provider Adapters (Metadata Ingestion) [APPROVED]

Two interfaces, because search/enrichment and availability have different
shapes and cadences:

```go
type MetadataProvider interface {
    Search(ctx context.Context, query string) ([]Candidate, error)
    Hydrate(ctx context.Context, providerID string) (*ItemDetails, error)
}

type AvailabilityProvider interface {
    Refresh(ctx context.Context, item *MediaItem) ([]Availability, error)
}
```

`Candidate` renders the add-flow picker: title, year, thumbnail URL,
provider ID, disambiguation line (director/author/platforms). `Hydrate`
returns canonical metadata, genres, cover URL, ratings, and availability
where the provider knows it. A registry maps `media_type →
MetadataProvider`; the HTTP layer never knows which upstream it talks to.

Registered metadata providers:

- **`tmdb`** (movies + TV): `/search/movie`, `/search/tv`; hydrate pulls
  details, genres, poster, and `/watch/providers` (JustWatch-backed
  availability, region US). One provider for both types.
- **`omdb`** is *not* a peer provider — it is a rating enricher invoked
  inside tmdb's `Hydrate`, keyed by the IMDB ID TMDB supplies. Contributes
  IMDB + Rotten Tomatoes rows to `ratings`. OMDb down ⇒ hydration succeeds
  without those rows.
- **`books`**: composition adapter. Search via Open Library (catalog
  breadth); hydrate takes the Open Library work then attempts ISBN/title
  match against Hardcover for community ratings. Hardcover miss ⇒
  metadata-only item, no error.
- **`igdb`**: search + hydrate against IGDB (covers, genres, IGDB rating,
  aggregate critic rating when present, external store links).

Availability/ownership enrichers:

- **`tmdbWatch`** — streaming availability, reuses the watch-providers call.
- **`gamecatalogs`** — quarantined unofficial Game Pass + PS+ catalog
  endpoints. Fetches each *full catalog* once per refresh cycle (~500 and
  ~800 titles respectively, a few paginated requests), caches the snapshot
  in the data dir, then matches locally against tracked games
  (normalization + IGDB alternative-names). Rationale: O(1) upstream
  requests per cycle regardless of library size; deterministic, offline-
  testable matching (per-item lookup would be a fuzzy store search, N
  unofficial calls per cycle, parsing their relevance ranking); newly added
  games match against the cached snapshot instantly. Residual cost:
  snapshot staleness bounded by refresh interval (weekly, acceptable given
  monthly catalog churn).
- **`steam`** — official `GetOwnedGames` for the configured Steam ID.
  Like `gamecatalogs`, the owned-games list is fetched once per cycle and
  cached, then matched per item via IGDB external-ID mapping, falling back
  to normalized-name match. Owned rows carry the web store-page URL.

Failure posture: every enricher is best-effort. A provider error degrades
the item (missing ratings/availability) and logs; it never blocks add or
refresh of other items. Unofficial endpoints get aggressive timeouts and a
circuit breaker so a dead endpoint cannot stall the weekly refresh.

API keys (TMDB, OMDb, IGDB client/secret, Hardcover, Steam) live in a
config file in the data dir — not env vars, never committed.

## Section 3 — Ingestion Pipeline & Background Refresh [APPROVED]

**Add flow (synchronous):** search → pick candidate → `Hydrate` → persist,
inline in the request with a ~10s budget. After the item row is persisted
(assigning its ID), cover art is downloaded to
`data-dir/covers/{item_id}.jpg` (resized to a max width; original URL kept
in `metadata` for re-fetch), ratings rows written, and availability
populated immediately — streaming from the same TMDB call,
games matched against the cached subscription-catalog snapshots. Failure of
a non-essential enrichment (OMDb, Hardcover, covers) saves the item with
gaps; only failure of the primary hydrate call aborts the add.

**Refresh job (asynchronous):** one goroutine on a `time.Ticker`, interval
from settings (default weekly), jittered start. Each cycle:

1. Re-fetch Game Pass + PS+ catalog snapshots (once per cycle, not per item).
2. Select items in `want_to`/`in_progress` ordered by `refreshed_at` asc.
3. Per item: re-run availability providers, refresh ratings, bump
   `refreshed_at`. Sequential with a small inter-item delay — no worker
   pool; a few hundred items completes in minutes, far under rate limits.
4. `done`/`abandoned` items skipped entirely (frozen historical record).

Manual triggers: a global "refresh now" on the settings page (e.g. after
changing subscriptions) and a per-item refresh on the detail page — both
run the same code path.

**Concurrency posture:** SQLite in WAL mode; refresher and HTTP handlers
share one `*sql.DB`. Writes are short per-item transactions, so the UI
never blocks behind a refresh cycle. No job queue, no cron table — a missed
cycle (box off) runs on next start if overdue.

**Startup behavior:** on boot, if the newest catalog snapshot or the oldest
active item's `refreshed_at` exceeds the interval, kick a refresh
immediately; otherwise wait for the ticker.

## Section 4 — HTTP Layer & View Contracts [APPROVED]

**UI specifics are explicitly deferred**: this section defines routes, view
*contents*, and interactions as a functional contract only. Layout,
card-vs-table, styling, and interaction polish will be explored in a
dedicated prototyping session (several throwaway variants, pick a winner)
before frontend implementation begins.

Route surface:

```
GET  /                         → landing page
GET  /{movies-tv|books|games}  → tab view: filterable/sortable collection
GET  /items/{id}               → detail page
GET  /search?type=&q=          → candidate picker partial
POST /items                    → add from candidate
POST /items/{id}/state         → lifecycle transition
POST /items/{id}/review        → verdict + completion date
PUT  /items/{id}/notes         → save notes
POST /items/{id}/refresh       → per-item re-enrichment
GET  /settings                 → services, keys status, refresh controls
POST /settings/services        → toggle subscriptions
POST /refresh                  → global refresh-now
GET  /covers/{id}.jpg          → cover art from data dir
```

**Landing page** (`/`) — cross-media overview:

- **Continue**: all `in_progress` items across the four types.
- **Newly available**: want-to items that gained availability on subscribed
  services since the previous refresh cycle (availability rows whose
  `first_seen_at` falls within the last cycle, joined to subscribed
  services).
- **Quick search** to add from anywhere.
- Compact per-tab counts (want-to / in-progress) as navigation.

**View contracts** (content, not presentation):

- *Tab views*: items for the media group; state filter (default *Want to*);
  Movies/TV tab gets an All/Movies/TV discriminator; sorts (added date,
  release year, aggregate rating, title); genre filter from present genres;
  **available-to-me** filter. All filter/sort state URL-encoded →
  bookmarkable views.
- *Detail page*: cover, core metadata, all rating sources with link-outs,
  availability with subscribed services distinguished and owned-link to
  store page, only-legal state transitions, verdict + completion date,
  Markdown notes with preview.
- *Add flow*: debounced search → candidates (thumbnail, title, year,
  disambiguation line) → one click adds; duplicate add navigates to the
  existing item with a flash.

**Technical posture** (backend-relevant regardless of UI outcome):
server-rendered templ + HTMX partials; static assets embedded via
`embed.FS` (offline-capable, no CDN); notes saved explicitly, no autosave.

## Section 5 — Error Handling, Degradation & Operations [APPROVED]

**Failure taxonomy**, three classes with distinct postures:

1. **Provider failures** (timeouts, 5xx, schema drift on unofficial
   endpoints): best-effort degradation — items save/refresh with gaps,
   never cascade. Availability/rating rows carry `fetched_at`, so staleness
   is inspectable, never silent. `gamecatalogs` gets a circuit breaker
   (skip for the rest of a cycle after consecutive failures); catalog
   snapshots are retained on fetch failure — stale beats none.
2. **User-input failures** (illegal state transition, unknown provider ID,
   malformed search): 4xx with HTMX-rendered inline error. Transition
   legality is a pure function `canTransition(from, to)`, exhaustively
   tested.
3. **System failures** (SQLite errors, full disk, unwritable cover dir):
   fail loudly — 500 with logged detail. No retries around a broken disk.

**Degradation surfaced, not hidden**: items whose availability exceeds 2×
the refresh interval render a staleness marker; circuit-broken providers
show on the settings page ("Game Pass catalog: last fetched 12 days ago,
failing"). The settings page doubles as the health dashboard —
per-provider last-success timestamps, configured-key status, snapshot
ages. No metrics stack; a status page + structured logs is the
observability story for a single-user tool. Provider health lives on the
settings page only (confirmed); the landing page stays purely media-
focused.

**Logging**: `log/slog`, JSON to stdout, captured by journald. Provider
calls at debug with duration; failures at warn with provider/item context;
one info summary line per refresh cycle (refreshed/failed/skipped counts).

**Operations**: one data dir (`~/.local/share/mediatracker/`, flag-
overridable): `app.db`, `covers/`, `catalogs/`, `config.toml`. Backup is
`sqlite3 .backup` + rsync — documented, not built. Migrations: embedded
sequential SQL applied at startup, tracked in `schema_version`; forward-
only, no rollback machinery. systemd unit with `Restart=on-failure` and
the data dir as `StateDirectory`.

## Section 6 — Testing Strategy [APPROVED]

**Guiding principle: meaningful, high-value tests over coverage
maximization.** Tests exist where behavior is subtle, failure is likely,
or breakage is expensive — parsing, matching, state legality, query
building — not to chase percentage targets or restate trivial code.

**Adapters — fixtures, never live APIs.** Each adapter carries `testdata/`
JSON captured from real responses (TMDB search result, IGDB hydrate
payload, Game Pass catalog snapshot, OMDb miss). Tests exercise parsing,
normalization, and edge cases — missing poster, absent ratings,
"Deluxe Edition" name-matching — entirely offline. Doubly valuable for
unofficial endpoints: on schema drift, re-capture one fixture, watch the
test fail, fix the adapter; the fixture documents the new shape.

**Unit layer** (pure functions, table-driven, only where logic is real):
state-transition legality (exhaustive 4×4), rating normalization across
source scales, game-name normalization/matching against catalog
snapshots, filter/sort query builder (URL params in → SQL + args out).

**Storage layer**: real SQLite (`:memory:`/temp file — mocking an embedded
engine tests nothing). Migrations-from-empty, idempotent re-add via the
unique constraint, and the availability-diff query behind "newly
available."

**HTTP layer**: `httptest` against the real mux, seeded temp DB, stub
providers injected via the registry. Covers route contracts: add flow
end-to-end (search → add → persisted item + cover written), transition
rejections, settings toggles affecting the available-to-me filter.

**Explicitly not built for MVP**: browser/E2E automation (server-rendered
views are already exercised through the real templates via `httptest`),
load testing, live-API tests in CI. Instead `cmd/probecheck`: a manual
utility firing one canned query per live provider and diffing response
shape — run when something smells stale, never a CI dependency.
