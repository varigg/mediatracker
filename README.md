# mediatracker

A self-hosted, single-user web app for tracking movies, TV shows, books,
and games through a want-to → in-progress → done/abandoned lifecycle —
with provider-sourced metadata, cover art, community ratings, and
streaming/subscription availability, all stored locally in SQLite.

One Go binary, no Node toolchain, no auth (your LAN or Tailscale is the
security boundary). Designed to run for years on a spare machine with a
weekly background refresh and a `sqlite3 .backup` as the whole ops story.

## Status

Mid-build. The storage core, all metadata/availability provider adapters,
and the ingestion + background-refresh orchestration are complete and
live-verified; the web frontend does not exist yet.

| Milestone | State |
|---|---|
| M1 Skeleton & storage core | done |
| M2 Metadata provider adapters | done |
| M3 Availability & ownership enrichers | done |
| M3.5 Live provider verification | done (TMDB/OMDb pending an account signup issue) |
| M4 Ingestion & refresh orchestration | done |
| M5 UI prototyping (gate) | next |
| M6 Frontend (templ + HTMX) | not started |
| M7 Ops hardening & release | not started |

Until M6 lands, the add/refresh pipelines are exercised through temporary
`/debug/*` HTTP endpoints (see `cmd/mediatracker/main.go`) and the app
serves only `GET /healthz` as a real route.

## Data sources

| Source | Used for | Key required |
|---|---|---|
| [TMDB](https://www.themoviedb.org) | Movie/TV metadata, covers, streaming availability | yes |
| [OMDb](https://www.omdbapi.com) | IMDB + Rotten Tomatoes ratings (enriches TMDB) | yes |
| [Open Library](https://openlibrary.org) | Book search + metadata | no |
| [Hardcover](https://hardcover.app) | Book community ratings | yes |
| [IGDB](https://www.igdb.com) | Game metadata, covers, ratings | yes (via Twitch) |
| Steam Web API | Game ownership | yes |
| Game Pass catalog | Game subscription availability | no (unofficial endpoint) |
| PS+ catalog | Deferred post-MVP (no discoverable API) | — |

All keys live in `config.toml` in the data dir — never in environment
variables, never committed.

## Getting started

```sh
go build ./cmd/mediatracker
./mediatracker            # data dir defaults to ~/.local/share/mediatracker
./mediatracker -data /path/to/dir
```

First boot creates the data dir, applies migrations to `app.db`, and
serves on `:8080`. Everything lives under the one data dir:

```
config.toml   # settings + API keys (never committed)
app.db        # SQLite, WAL mode
covers/       # downloaded cover art, {item_id}.jpg
catalogs/     # Game Pass / Steam ownership snapshots
```

### config.toml

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

Every key is optional — a provider without its key simply doesn't
register, and the rest of the app works around it. Books need no key at
all (Hardcover only enriches ratings).

### Obtaining keys

- **TMDB**: themoviedb.org → Settings → API → request a developer key
  (v3 auth). Free for personal use.
- **OMDb**: omdbapi.com/apikey.aspx → free tier → activate via the
  emailed link.
- **IGDB**: rides on Twitch. Enable 2FA on your Twitch account, then
  dev.twitch.tv/console → Register Your Application (OAuth redirect
  `https://localhost`, category Application Integration) → copy the
  Client ID and generate a Client Secret.
- **Hardcover**: hardcover.app → Settings → Hardcover API → copy the
  bearer token (without the `Bearer ` prefix).
- **Steam**: steamcommunity.com/dev/apikey (domain may be `localhost`).
  `steam_id` is your 64-bit SteamID. Your Steam profile's *Game details*
  privacy setting must be **Public** for the owned-games list to return
  anything.

### Verifying live providers

`cmd/probecheck` fires one canned query per configured provider and
prints the resulting shapes — useful after filling in keys or when an
upstream API smells stale. It is a manual utility, never run by tests or
CI:

```sh
go run ./cmd/probecheck
```

## Development

```sh
go test ./...                                # fixtures only, no live APIs
git config core.hooksPath .githooks         # enable the pre-commit hook
```

The pre-commit hook and CI both run `gofmt` + `go vet` + `go test`
(CI adds `-race`). Adapter tests run against `testdata/` fixtures
exclusively; live-shape verification is `probecheck`'s job.

Design docs, milestone plans, and per-session implementation plans live
under `docs/superpowers/` — start with
`specs/2026-07-06-media-tracker-design.md` for the full design and
`plans/2026-07-06-mediatracker-mvp.md` for the milestone roadmap. See
[AGENTS.md](AGENTS.md) for coding conventions and architecture notes.
