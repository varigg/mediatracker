# Media Tracker MVP Implementation Plan

> **For agentic workers:** This is a MILESTONE-LEVEL plan by explicit user
> directive. Do NOT execute it directly. At the start of each implementation
> session, expand the next milestone into a bite-sized task plan (per
> superpowers:writing-plans conventions: failing test → verify → implement →
> verify → commit, complete code in every step) in
> `docs/superpowers/plans/sessions/`, get user sign-off, then execute via
> superpowers:subagent-driven-development or superpowers:executing-plans.
> Spec: `docs/superpowers/specs/2026-07-06-media-tracker-design.md` — read
> it in full before expanding any milestone.

**Goal:** Self-hosted, single-user web app tracking movies/TV, books, and
games through a want-to → in-progress → done/abandoned lifecycle, with
provider-sourced metadata, cover art, ratings, and streaming/subscription
availability.

**Architecture:** Unified `media_items` model with promoted filter columns
plus JSON residue; per-media-type `MetadataProvider` adapters and
cycle-cached `AvailabilityProvider` enrichers behind registries; single
background refresh goroutine; server-rendered templ + HTMX frontend.

**Tech Stack:** Go, SQLite (WAL), templ, HTMX, `log/slog`, `embed.FS`.
Bare binary + systemd. No Node toolchain.

## Global Constraints

- Official APIs only, except Game Pass / PS+ catalogs (unofficial,
  quarantined in the `gamecatalogs` adapter, circuit-broken).
- API keys in `config.toml` in the data dir — never env vars, never
  committed; never read `.env` files.
- Adapter tests run against `testdata/` fixtures, never live APIs.
- Meaningful, high-value tests over coverage maximization.
- Provider failures degrade, never cascade; system failures fail loudly.
- All state (db, covers, catalog snapshots, config) under one data dir.
- Conventional Commits; no AI attribution anywhere.
- Frontend implementation (M6) MUST NOT start before the UI prototyping
  session (M5) has produced a picked winner.

---

## Milestone 1 — Skeleton & Storage Core

**Deliverable:** `mediatracker` binary that boots, creates/migrates the
SQLite schema in a data dir, loads `config.toml`, and serves a health
endpoint. Store layer with typed CRUD, state-transition enforcement, and
the filter/sort query builder.

**Scope:** Go module layout (`cmd/mediatracker`, `internal/store`,
`internal/config`); embedded sequential migrations + `schema_version`;
schema v1 exactly as specced (media_items, ratings, availability with
`first_seen_at`, services seeded with US catalog, settings); WAL mode;
`canTransition` pure function; query builder (URL-param-shaped input →
SQL + args) covering state/type/genre/available-to-me filters and the four
sorts.

**Key tests:** migrations from empty; exhaustive 4×4 transition table;
idempotent re-add via `UNIQUE(provider, provider_id)`; query-builder
table-driven cases; availability upsert preserves `first_seen_at`.

## Milestone 2 — Metadata Provider Adapters

**Deliverable:** `Search`/`Hydrate` working against all four media types,
exercised by fixtures; `cmd/probecheck` for manual live-shape verification.

**Scope:** `MetadataProvider` interface + registry (`internal/providers`);
`tmdb` (movies+TV, with `omdb` rating enricher inside Hydrate, keyed by
IMDB ID); `books` (Open Library search/hydrate + Hardcover rating match,
miss-tolerant); `igdb` (Twitch OAuth client-credentials flow); rating
normalization to 0–100 with original `display` string; fixture capture for
each provider including degenerate cases (missing poster, absent ratings,
OMDb miss, Hardcover miss).

**Key tests:** parse/normalize per fixture; enricher-failure degradation
(hydrate succeeds with gaps); normalization table across source scales.

## Milestone 3 — Availability & Ownership Enrichers

**Deliverable:** availability rows produced for all three sources from
cached cycle data; game-name matcher working against catalog snapshot
fixtures.

**Scope:** `AvailabilityProvider` interface; `tmdbWatch` (region US,
kind=stream/subscription); `gamecatalogs` — full-catalog snapshot fetch
(Game Pass + PS+), disk cache in `catalogs/`, retained on fetch failure,
circuit breaker; `steam` — `GetOwnedGames` once per cycle, IGDB
external-ID mapping with normalized-name fallback, `kind=owned` with web
store-page URL; shared name normalizer (editions, casefold, alternative
names).

**Key tests:** matcher against snapshot fixtures ("Deluxe Edition" cases);
circuit-breaker trip/skip; stale-snapshot retention on fetch failure.

## Milestone 3.5 — Live Provider Verification

**Deliverable:** `cmd/probecheck` green against every configured live
provider; fixtures re-captured wherever real responses drift from the
hand-authored shapes; `gamecatalogs` Game Pass fetcher wired to the real
endpoint. **PS+ is consciously deferred to the post-MVP backlog**
(decided 2026-07-07, see below) — its placeholder stays in place and its
circuit breaker simply keeps failing/skipping every cycle, which the
existing degrade-never-cascade design already tolerates.

**Closed out 2026-07-07 with TMDB/OMDb still unconfigured** (user's
TMDB signup is blocked by a CloudFront challenge, unrelated to this
codebase) — the user chose to proceed to M4 rather than wait. This is
safe: `registry.Get` degrades an unconfigured provider to "skipped, not
configured" rather than an error, so `probecheck` already exits clean
with TMDB/OMDb absent. IGDB, Hardcover, Game Pass, and Steam are all
verified live. Revisit TMDB/OMDb whenever the signup clears — no code
changes are anticipated, just re-running `probecheck` and folding back
any drift, same as the rest of this milestone.

**Scope:** interactive session with the user (API keys are theirs; keys
go into `config.toml` in the data dir and are NEVER committed, echoed
into the transcript, or read from `.env`). Steps: user acquires keys
(prerequisites below) → user fills `config.toml` → run `probecheck` →
diff live shapes against `testdata/` fixtures → fix adapters and
re-capture fixtures where drifted → identify and wire the real Game Pass
catalog endpoint, replacing the placeholder URL and shape from M3 →
re-run until green (PS+ excluded — see Post-MVP Backlog). Any adapter
change follows the usual TDD cycle in a worktree.

**Prerequisites — obtaining keys** (user-facing instructions, also the
seed for M7's README):

- **TMDB** (`tmdb_key`): account at themoviedb.org → Settings → API →
  request a developer API key (v3 auth). Free for personal use.
- **OMDb** (`omdb_key`): omdbapi.com/apikey.aspx → free tier (1,000
  req/day) → activate via the emailed link.
- **IGDB** (`igdb_client_id`, `igdb_client_secret`): IGDB auth rides on
  Twitch. Enable 2FA on your Twitch account, then dev.twitch.tv/console
  → Register Your Application (OAuth redirect `https://localhost`,
  category Application Integration) → copy Client ID and generate a
  Client Secret.
- **Hardcover** (`hardcover_key`): account at hardcover.app → Settings →
  Hardcover API → copy the bearer token.
- **Steam** (`steam_key`, `steam_id`): steamcommunity.com/dev/apikey
  (domain field may be `localhost`). `steam_id` is your 64-bit SteamID
  (steamid.io or your profile URL). Game details in your Steam privacy
  settings must be public for `GetOwnedGames` to return your library.
- **Game Pass**: no keys — unofficial endpoint; this milestone discovers
  and validates the real request/response shapes.
- **PS+**: no keys, but deferred to the post-MVP backlog — the real
  Game Catalog page renders its grid client-side with no discoverable
  API call in static HTML/JS; the placeholder ships as-is through MVP.

`config.toml` shape (values are placeholders):

```toml
listen_addr = ":8080"
log_level = "info"
refresh_interval = "168h"

[providers]
tmdb_key = "..."
omdb_key = "..."
igdb_client_id = "..."
igdb_client_secret = "..."
hardcover_key = "..."
steam_key = "..."
steam_id = "7656119..."
```

**Key tests:** none new in CI — this milestone's output is verified
adapters and updated fixtures; existing fixture tests must stay green
after every re-capture.

## Milestone 4 — Ingestion & Refresh Orchestration

**Deliverable:** end-to-end add pipeline and the weekly refresher, both
invocable (add via a temporary CLI/HTTP stub if M6 not yet started).

**Scope:** add flow — hydrate (~10s budget) → persist → cover download to
`covers/{item_id}.jpg` (resize, original URL into metadata) → ratings +
availability writes, non-essential failures tolerated; refresh goroutine —
jittered ticker, per-cycle catalog snapshot refresh, active items by
`refreshed_at` asc, sequential with inter-item delay, done/abandoned
skipped; startup catch-up when overdue; manual global + per-item refresh
entry points; "newly available" diff query (`first_seen_at` within last
cycle ∧ subscribed); per-cycle slog summary line.

**Key tests:** add-flow degradation matrix (cover/OMDb/availability fail ⇒
item persists with gaps; primary hydrate fail ⇒ abort); refresh-cycle
behavior with stub providers; newly-available query.

## Milestone 5 — UI Prototyping Session (GATE)

**Deliverable:** a picked winner among several throwaway UI variants, and
an updated view-contract addendum in the spec if prototyping changes any
contract.

**Scope:** interactive session with the user (not subagent-executed):
static/mock-data prototypes of landing page, tab view (filters/sorts,
card-vs-table), and detail page; explore layout, density, and HTMX
interaction patterns. Decisions recorded in
`docs/superpowers/specs/` as an addendum. No production code.

## Milestone 6 — Frontend Implementation

**Deliverable:** the full route surface from the spec, rendering the
winning prototype's design; app fully usable end to end.

**Scope:** templ components per the picked design; all routes from the
spec's route table; tab views with URL-encoded filter/sort state; detail
page (ratings link-outs, availability with subscribed highlight and owned
store links, legal-transitions-only, verdict + completion date, Markdown
notes with preview, explicit save); debounced add-flow search with
duplicate-add redirect + flash; landing page (Continue, Newly available,
quick search, per-tab counts); settings page (service checklist, key
status, provider health, refresh-now); static assets via `embed.FS`;
covers served from data dir.

**Key tests:** `httptest` route contracts with stub providers + seeded
temp DB — add flow end-to-end, transition rejections, subscription toggle
affecting available-to-me filter.

## Milestone 7 — Ops Hardening & Release

**Deliverable:** installable release: systemd unit, documented backup and
config, staleness surfaced in UI.

**Scope:** systemd unit (`Restart=on-failure`, `StateDirectory`);
README covering config.toml schema, API-key acquisition (TMDB, OMDb, IGDB
client/secret, Hardcover, Steam), backup (`sqlite3 .backup` + rsync);
staleness markers (availability > 2× interval); settings-page health
completeness (per-provider last-success, snapshot ages, key status); log
level configuration; final `probecheck` pass against live providers.

---

## Milestone Dependency Order

M1 → M2 → M3 → M3.5 → M4 → M5 (gate) → M6 → M7. M2 and M3 may interleave
after M1 if a session prefers, but M4 requires both. M3.5 (live
verification, interactive) requires M2+M3 and should precede M4 so the
ingestion pipeline is built on verified adapters; M7's final probecheck
pass then only re-confirms. M5 may run any time after M1 but must
complete before M6 begins.

## Post-MVP Backlog

- **PS+ Game Catalog live endpoint** (deferred 2026-07-07, during
  M3.5). The consumer-facing catalog (~400 titles, PS+ Extra/Premium)
  lives at `playstation.com/en-us/ps-plus/games/` and renders
  client-side with no API call visible in the static HTML/JS — finding
  it requires a browser-captured network trace, which wasn't available
  in the M3.5 session. A dead end was ruled out: the PS Store's
  reverse-engineered GraphQL API (`categoryGridRetrieve` on
  `web.np.playstation.com`, via the OSS project
  `mrt1m/playstation-store-api`) has a `PS_PLUS` category id
  (`038b4df3-bb4c-48f8-8290-3feb35f0f0fd`) that resolves to the legacy
  "PS Plus Monthly Games" list, not the Extra/Premium catalog — that id
  is not the answer. Until solved, `gamecatalogs`' PS+ fetcher stays on
  the M3 placeholder URL, failing every cycle; this is safe (breaker +
  stale-snapshot retention already handle it) but means PS+ ownership
  rows are never produced. Pick up by capturing the real network
  request from a browser session on that page.

## Self-Review Notes

Spec coverage verified section-by-section: data model → M1; adapters →
M2/M3; ingestion/refresh → M4; HTTP/view contracts → M6 (landing page
included); error handling/degradation → distributed (M2 degradation, M3
circuit breaker, M4 degradation matrix, M7 staleness surfacing); ops →
M1 (data dir/migrations) + M7; testing strategy → embedded per milestone
as "key tests". UI-deferral directive honored via M5 gate.
