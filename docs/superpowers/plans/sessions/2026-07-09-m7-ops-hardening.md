# M7 — Ops Hardening & Release Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps
> use checkbox (`- [ ]`) syntax. Task briefs are spec-style; the
> controller expands them at dispatch.

**Goal:** An installable release: systemd unit + a README a stranger can
follow from clone to running service (user-requested install guide),
staleness surfaced in the UI, settings-page health completeness, the
TMDB alias refresh, backlog bookkeeping, and the final live probecheck
pass — closing the MVP.

## Global Constraints

- No new Go dependencies. Every established convention holds (errors,
  logging, fixtures-only tests, Conventional Commits, no AI attribution).
- Staleness threshold per spec §5: availability `fetched_at` older than
  **2× refresh_interval** renders a marker. Threshold derives from
  config — never hardcoded.
- Health data on settings must not add hot-path cost beyond one settings
  upsert per successful provider operation.
- Phone-layout polish is explicitly OUT (post-MVP; needs interactive
  visual iteration).

## Design Decisions (flagged for sign-off)

1. **Per-provider last-success tracking** (spec's settings-health item):
   recorded as settings rows `provider_last_success_{name}` (SQLite
   TEXT, `store.TimeFormat`) written by `ingest` after each successful
   `Hydrate` (add + refresh paths) and by the availability syncers'
   wrapper after successful `SyncCycle` — implemented as a small
   `ingest` helper so `server` only reads settings. Names: `tmdb`,
   `openlibrary`, `igdb` (metadata, from `ItemDetails.Provider`) and
   `game_pass`/`steam_owned` implicitly via snapshot ages instead
   (decision 2) — no double bookkeeping.
2. **Snapshot ages from the files themselves**: settings page reads
   `catalogs/{game_pass,ps_plus,steam_owned}.json`, parsing the
   `fetched_at` field (missing file → "never"). Server-side helper in
   `internal/server` using `Deps.DataDir`; no new store surface.
3. **Staleness marker placement**: detail-page availability chips get a
   `stale` class + "(stale)" suffix when `fetched_at` breaches the
   threshold; the settings Refresh section shows the same computation
   summarized ("N availability rows stale"). Tab rows stay clean
   (density) — chips there already summarize; staleness is a
   detail/settings concern.
4. **Alias refresh**: add observed TMDB watch-provider variants to
   `providerSlugs` — "Netflix Standard with Ads" → `netflix`, plus the
   ad-tier variants pattern for the other seeded services where TMDB
   publishes them ("Max With Ads"? — implementer verifies against
   TMDB's current provider list ONLY via the committed fixture +
   probecheck observations recorded here: netflix_standard_with_ads
   was observed live 2026-07-09; do not invent unobserved aliases, add
   the observed one and leave a comment inviting future additions).
5. **systemd unit ships as `deploy/mediatracker.service`** (repo file,
   documented in README): `Type=simple`, `Restart=on-failure`,
   `StateDirectory=mediatracker`, `ExecStart=... -data
   /var/lib/mediatracker`, hardening basics (`NoNewPrivileges`,
   `ProtectSystem=strict` + `ReadWritePaths` via StateDirectory,
   `ProtectHome=read-only`... keep to well-understood directives).
6. **README restructure** with the user-requested install guide:
   Install (prereqs Go ≥1.26, clone+build, or `go install .../cmd/...@latest`),
   First run (config.toml creation, keys — existing acquisition section
   retained, browse to :8080), Run as a service (systemd unit install
   steps), Upgrade (git pull, rebuild, restart), Backup & restore
   (`sqlite3 .backup` + rsync of covers/config; restore steps),
   Configuration reference (all config.toml fields incl. log_level
   values), Health & troubleshooting (settings page, log reading,
   probecheck). Status table updated to complete.
7. **Backlog bookkeeping**: plan doc's Post-MVP Backlog gains Cloudflare
   Tunnel+Access and Plex/*arr entries (from memory, recorded
   2026-07-09); phone-layout polish moves there explicitly.

## Tasks

### Task 1: Staleness plumbing & markers
`internal/server`: threshold = 2×`Deps.RefreshInterval`; parse
`Availability.FetchedAt` via `store.TimeFormat`; `AvailChip` gains
`Stale bool`; detail template renders `(stale)` suffix + `.stale` CSS
(muted italic); settings Refresh section shows count of stale rows
(store: one new query `CountStaleAvailability(ctx, cutoff time.Time)
(int, error)` — wrapped errors, TDD'd). Tests: chip staleness via
seeded old `fetched_at` (store test helper to backdate a row — direct
SQL in test), settings count, fresh rows unmarked.

### Task 2: Provider health — last-success + snapshot ages
`ingest`: record `provider_last_success_{provider}` after successful
Hydrate (both paths — extract one unexported helper) and after each
successful `SyncCycle` per syncer... syncers don't expose names —
record only metadata providers here; snapshot ages cover the sync side
(decision 1/2). `server`: settings Providers section gains "last
success" per metadata provider (settings read; "never" fallback) and
snapshot-age lines for game_pass/ps_plus/steam_owned (file parse,
"never" when missing; ps_plus will honestly show never/stale). Tests:
last-success written on add & refresh; settings renders ages from
fixture snapshot files written into the test DataDir.

### Task 3: Alias refresh + log-level verification
`tmdb/watch.go` providerSlugs: add "Netflix Standard with Ads" →
netflix (observed live 2026-07-09) + comment. Verify log_level config
plumbs end to end (it does — main.go UnmarshalText; add a config test
asserting invalid level errors at boot if missing). Small task, folds
into Task 2's commit window if trivial — keep separate commit.

### Task 4: systemd unit + README + backlog docs
`deploy/mediatracker.service` per decision 5; README restructure per
decision 6 (install guide is the user-requested centerpiece); MVP plan
doc: Post-MVP Backlog additions per decision 7; status table + AGENTS.md
CI line already accurate — touch only if drifted. No code.

### Task 5: Integration sweep, boot smoke, final probecheck
CI parity + covercheck; boot smoke incl. staleness/health rendering
paths (fresh dir: everything "never", no stale rows, exit clean);
`go run ./cmd/probecheck` against live providers (controller runs —
keys live in the user's data dir) — the milestone's final gate;
ledger close-out.

## Self-Review Notes
Spec M7 scope ↔ tasks: systemd (T4), README config/keys/backup (T4),
staleness markers (T1), settings health completeness (T2 + existing key
status), log level (T3), final probecheck (T5). User addition: install
guide (T4). Session additions: alias refresh (T3), backlog docs (T4).
Explicitly out: phone polish (backlog), per-provider last-success for
availability syncers (covered by snapshot ages — recorded rationale).
