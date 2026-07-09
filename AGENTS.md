# AGENTS.md — context for coding agents

Read this before changing code. The full design is in
`docs/superpowers/specs/2026-07-06-media-tracker-design.md`; the milestone
roadmap is `docs/superpowers/plans/2026-07-06-mediatracker-mvp.md`; each
completed milestone has a task-level plan under
`docs/superpowers/plans/sessions/`. When your change contradicts a design
decision recorded there, surface it — don't silently override it.

## Architecture map

```
cmd/mediatracker      binary: flag parsing, config load, dependency wiring
cmd/probecheck        manual live-API verification; never in CI, never polished
internal/config       config.toml loading (data dir); keys live here only
internal/store        SQLite: migrations, typed CRUD, lifecycle transitions,
                      ListFilter query builder. Owns shared vocabulary:
                      TimeFormat, Kind, MediaType/State/Verdict, sentinels.
internal/providers    MetadataProvider + AvailabilityProvider contracts,
                      registry, rating normalization, shared metadata keys.
  ├─ tmdb             movies/TV (+ OMDb rating enrichment inside Hydrate)
  ├─ books            Open Library (+ Hardcover rating enrichment)
  ├─ igdb             games (Twitch client-credentials OAuth)
  ├─ gamecatalogs     unofficial Game Pass/PS+ catalogs — QUARANTINED:
  │                   circuit breaker, aggressive timeouts, snapshots
  ├─ steam            ownership via GetOwnedGames, snapshot-cached
  ├─ names            game-title normalization for catalog matching
  └─ setup            composition root (outside providers: import cycle)
internal/covers       cover download → resize (600px max width) → JPEG
internal/ingest       orchestration: Add (synchronous add-flow) and
                      Refresher (weekly cycle, startup catch-up)
internal/server       HTTP layer: full route surface (tabs, detail, add,
                      settings, covers) rendered via html/template + HTMX
```

Dependency direction is strict: `store` is a leaf; adapters depend on
`providers` + `store`; `ingest` sits on top. Never make `providers`
import an adapter.

## Invariants — do not break these

- **Failure taxonomy** (spec §5): provider/enrichment failures *degrade*
  (log + continue, item saved with gaps); system failures (SQLite, disk)
  *fail loudly* (propagate). Only a primary `Hydrate` failure aborts an
  add. Refresh cycles never abort on a per-item failure.
- **Frozen items**: done/abandoned items are never touched by any refresh
  path. `store.State.Active()` is the single Go-side definition.
- **Stale beats none**: catalog/ownership snapshots are retained on fetch
  failure. `gamecatalogs` is the only sanctioned unofficial-API code and
  stays quarantined behind its circuit breaker.
- **Ratings are delete-then-insert** (`ReplaceRatings`) — never call it
  with an empty slice on the refresh path; an empty successful Hydrate
  means "nothing new", not "delete everything".
- **`ingest.Add` is the sole writer of `metadata["cover_url"]`**; adapters
  populate the typed `ItemDetails.CoverURL` field only. The game-metadata
  keys are the `providers.MetadataKey*` constants — no bare literals.
- **One timestamp format**: `store.TimeFormat`. Never re-declare
  `"2006-01-02 15:04:05"`.
- **API keys** live in `config.toml` in the data dir. Never env vars,
  never committed, never echoed into logs/transcripts. Never read `.env`
  files.

## Conventions

- **Errors**: wrap at exported boundaries with `"pkg: operation: %w"`
  (`"store: get item: %w"`). Sentinels carry the prefix in their text
  (`store.ErrNotFound`, `ingest.ErrItemNotActive`). Don't double-wrap:
  internal helpers and pass-through calls stay bare when the caller
  already wraps once. Callers match with `errors.Is`, never string
  comparison.
- **Logging**: injected `*slog.Logger` everywhere (adapter `WithLogger`
  option / `ingest.Deps.Logger` / `server.New` parameter) — never the
  package-global `slog` outside `main`. Structured attrs: `"error"`,
  `"item_id"`, `"media_type"`, `"catalog"`.
- **Adapters** follow one shape: `New(keys..., opts ...Option)`,
  `WithHTTPClient`/`WithLogger`/`WithBaseURL`-style options for test
  injection, unexported response structs, self-filtering `Refresh` that
  returns `(nil, nil)` for items it doesn't handle.
- **Commits**: Conventional Commits (`feat:`, `fix:`, `refactor:`,
  `docs:`, `chore:`); imperative subject; body explains *why*, wrapped at
  72 columns. **No AI attribution anywhere** — no Co-Authored-By
  trailers, no "generated with" footers, in commits, PRs, or comments.
- **Workflow**: feature work happens in a git worktree under
  `.worktrees/{branch}`, merged via PR. Milestones are expanded into a
  session plan (TDD steps) under `docs/superpowers/plans/sessions/` and
  signed off before implementation.

## Testing

- **TDD**: failing test → verify it fails → implement → verify it passes.
- **Never hit live APIs in tests.** Adapters test against `testdata/`
  JSON fixtures served by `httptest`; on upstream drift, re-capture the
  fixture (that's what `cmd/probecheck` is for). Store tests use real
  SQLite in `t.TempDir()` — no mocks of the database. Orchestration tests
  use stub providers.
- Meaningful, high-value tests over coverage maximization. Test names
  document behavior (`TestRunCycleDoesNotWipeRatingsOnEmptyHydrate`).
- Verify before claiming done:

```sh
gofmt -l . && go vet ./... && go test ./... -count=1
```

CI (`.github/workflows/ci.yml`) runs the same plus `go build`, `-race`,
and coverage floors via go-covercheck (`.go-covercheck.yml`). The
floors are regression ratchets set just under current reality — if one
trips, coverage dropped materially; never write filler tests to satisfy
them. The pre-commit hook (`.githooks/`, opt-in via
`git config core.hooksPath .githooks`) mirrors the fmt/vet/test steps.

## Gotchas

- `modernc.org/sqlite` converts columns declared `DATE`/`DATETIME` to
  `time.Time` at scan time; this schema deliberately declares timestamp
  columns `TEXT` so Go models can use plain strings. Keep it that way.
- TMDB `provider_id`s are namespaced (`"movie:603"`, `"tv:1396"`) because
  movie and TV IDs collide and `UNIQUE(provider, provider_id)` uses one
  `tmdb` provider value for both.
- `availability.service_slug` has no foreign key *by design* — enrichers
  may emit services beyond the seeded catalog; unknown slugs simply never
  count as subscribed.
- The Game Pass fetch is a two-step call (sigls list → displaycatalog
  hydration, batched ≤100 IDs); the PS+ URL is a known-dead placeholder,
  deferred post-MVP (see the plan's Post-MVP Backlog for findings).
- `ActiveItemsByRefreshDue`'s SQL state filter must stay in sync with
  `store.State.Active()` by hand — SQL can't call Go.
