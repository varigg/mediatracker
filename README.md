# mediatracker

A self-hosted, single-user web app for tracking movies, TV shows, books,
and games through a want-to → in-progress → done/abandoned lifecycle —
with provider-sourced metadata, cover art, community ratings, and
streaming/subscription availability, all stored locally in SQLite.

One Go binary, no Node toolchain, no auth (your LAN or Tailscale is the
security boundary). Designed to run for years on a spare machine with a
weekly background refresh and a `sqlite3 .backup` as the whole ops story.

## Status

MVP complete. Storage core, all metadata/availability provider adapters,
ingestion + background-refresh orchestration, the full html/template +
HTMX frontend, and ops hardening (systemd unit, staleness surfacing,
provider-health settings page) are done and live-verified.

| Milestone | State |
|---|---|
| M1 Skeleton & storage core | done |
| M2 Metadata provider adapters | done |
| M3 Availability & ownership enrichers | done |
| M3.5 Live provider verification | done (TMDB/OMDb pending an account signup issue) |
| M4 Ingestion & refresh orchestration | done |
| M5 UI prototyping (gate) | done |
| M6 Frontend (html/template + HTMX) | done |
| M7 Ops hardening & release | done |

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

## Install

Prerequisites: Go ≥ 1.26. The `sqlite3` CLI is optional — only needed if
you want to take live backups with `.backup` (see below).

Build from a clone:

```sh
git clone https://github.com/varigg/mediatracker.git
cd mediatracker
go build ./cmd/mediatracker
```

Or install straight from the module, no clone required:

```sh
go install github.com/varigg/mediatracker/cmd/mediatracker@latest
```

Either way you end up with a single `mediatracker` binary and nothing
else to install — no Node toolchain, no external services. Clone+build
drops it in the repo root as `./mediatracker`; `go install` puts it in
`$(go env GOPATH)/bin` (usually `~/go/bin`), so it's `mediatracker` if
that directory is on your `PATH`.

## First run

```sh
mediatracker                     # data dir defaults to ~/.local/share/mediatracker
mediatracker -data /path/to/dir  # or pick one explicitly
```

(From a clone+build, that's `./mediatracker` in the repo root.)

`-data` is the binary's only flag. On startup it creates the data
directory if missing and opens/migrates `app.db` (SQLite, WAL mode)
inside it. It does **not** write a `config.toml` for you — a missing
config file just means "run with defaults" (`listen_addr = ":8080"`,
`log_level = "info"`, `refresh_interval = "168h"`, no provider keys). To
customize anything, including adding API keys, create `config.toml`
yourself in the data dir. Everything the app owns lives under that one
directory:

```
config.toml   # settings + API keys (never committed)
app.db        # SQLite, WAL mode
covers/       # downloaded cover art, {item_id}.jpg
catalogs/     # Game Pass / Steam ownership snapshots
```

Minimal `config.toml` to add provider keys (see the [Configuration
reference](#configuration-reference) for every field):

```toml
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
all (Hardcover only enriches ratings). Restart the process after editing
`config.toml`; it's read once at startup.

Browse to `http://localhost:8080` once it's running.

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
- **Game Pass**: no key — the catalog sync uses an unofficial endpoint.
- **PS+**: not wired up yet; see the Post-MVP Backlog in
  `docs/superpowers/plans/2026-07-06-mediatracker-mvp.md`.

## Run as a service (systemd)

The repo ships `deploy/mediatracker.service`. It expects the binary at
`/usr/local/bin/mediatracker`, runs as a dedicated `mediatracker` system
user, and keeps its data under `/var/lib/mediatracker` via
`StateDirectory` (systemd creates and owns that directory for you).

```sh
# one-time setup (from the repo root of a clone)
sudo useradd --system --home-dir /var/lib/mediatracker --shell /usr/sbin/nologin mediatracker
sudo cp mediatracker /usr/local/bin/mediatracker
sudo cp deploy/mediatracker.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now mediatracker
```

If you installed via `go install` instead, copy the binary from
`$(go env GOPATH)/bin` — `sudo cp "$(go env GOPATH)/bin/mediatracker"
/usr/local/bin/mediatracker` — and fetch the unit file from the repo,
since you won't have a `deploy/` directory locally (clone the repo, or
`curl -fLO https://raw.githubusercontent.com/varigg/mediatracker/main/deploy/mediatracker.service`
and copy that into `/etc/systemd/system/`).

Put your `config.toml` (with API keys) in `/var/lib/mediatracker/` —
create it as root or `chown` it to `mediatracker` after writing it, then
restart the unit for it to take effect:

```sh
sudo systemctl restart mediatracker
```

Logs go to the journal:

```sh
journalctl -u mediatracker -f
```

The unit hardens the process with `NoNewPrivileges=true`,
`ProtectSystem=strict`, and `ProtectHome=read-only`; `StateDirectory`
is the one writable path carved out of the read-only root.

## Upgrade

```sh
git pull
go build ./cmd/mediatracker
sudo cp mediatracker /usr/local/bin/mediatracker
sudo systemctl restart mediatracker
```

Or, if you installed via `go install`, re-run
`go install github.com/varigg/mediatracker/cmd/mediatracker@latest`,
copy the resulting binary into place, and restart. Migrations run
automatically against `app.db` on the next startup — there's no manual
migration step.

## Backup & restore

Everything worth backing up lives in the data dir. With the service
running, SQLite's own backup command produces a consistent snapshot
without stopping anything:

```sh
sqlite3 /var/lib/mediatracker/app.db ".backup '/path/to/backup/app.db'"
rsync -a /var/lib/mediatracker/covers/ /path/to/backup/covers/
cp /var/lib/mediatracker/config.toml /path/to/backup/config.toml
```

`catalogs/` (Game Pass/Steam snapshots) is disposable cache and doesn't
need backing up — it's rebuilt on the next refresh cycle.

To restore: stop the service, copy the backed-up `app.db`, `covers/`,
and `config.toml` back into the data dir, then start the service again.

```sh
sudo systemctl stop mediatracker
cp /path/to/backup/app.db /var/lib/mediatracker/app.db
rsync -a /path/to/backup/covers/ /var/lib/mediatracker/covers/
cp /path/to/backup/config.toml /var/lib/mediatracker/config.toml
sudo systemctl start mediatracker
```

## Configuration reference

All fields live in `config.toml` in the data dir. A missing file (or a
missing individual field) falls back to the default shown. Never commit
this file — API keys live here.

| Field | Type | Default | Meaning |
|---|---|---|---|
| `listen_addr` | string | `":8080"` | HTTP listen address (`host:port` or `:port`). |
| `log_level` | string | `"info"` | One of `debug`, `info`, `warn`, `error` (case-insensitive). An invalid value fails startup. |
| `refresh_interval` | duration string | `"168h"` | How often the background refresher re-syncs active items; any `time.ParseDuration` value (e.g. `"24h"`, `"72h"`). |
| `providers.tmdb_key` | string | unset | TMDB v3 API key. |
| `providers.omdb_key` | string | unset | OMDb API key. |
| `providers.igdb_client_id` | string | unset | Twitch/IGDB OAuth client ID. |
| `providers.igdb_client_secret` | string | unset | Twitch/IGDB OAuth client secret. |
| `providers.hardcover_key` | string | unset | Hardcover bearer token. |
| `providers.steam_key` | string | unset | Steam Web API key. |
| `providers.steam_id` | string | unset | 64-bit SteamID whose owned-games list to read. |

Every `providers.*` field is independently optional; each provider
registers only when its required keys are present, and the app degrades
gracefully around any that aren't configured.

## Health & troubleshooting

The **Settings** page (`/settings`) is the first stop: it shows, per
metadata provider, whether it's configured and its last successful
fetch; per availability catalog (Game Pass, PS Plus, Steam owned), the
snapshot's age; and a count of stale availability rows (fetched more
than 2× `refresh_interval` ago). It also has a manual "Refresh now"
button.

For anything the settings page doesn't explain, read the logs — the
server logs structured JSON to stdout (`journalctl -u mediatracker -f`
under systemd), including a startup line with the listen address and
data dir, and per-item errors during add/refresh.

To sanity-check a specific provider's live behavior directly (bypassing
the app), run the manual probe utility:

```sh
go run ./cmd/probecheck
```

It fires one canned query per configured provider and prints the
resulting shapes — useful after filling in keys or when an upstream API
smells stale. It's a manual utility, never run by tests or CI.

## Development

```sh
go test ./...                                # fixtures only, no live APIs
git config core.hooksPath .githooks         # enable the pre-commit hook
```

The pre-commit hook and CI both run `gofmt` + `go vet` + `go test`
(CI adds `-race` and coverage floors). Adapter tests run against
`testdata/` fixtures exclusively; live-shape verification is
`probecheck`'s job.

Design docs, milestone plans, and per-session implementation plans live
under `docs/superpowers/` — start with
`specs/2026-07-06-media-tracker-design.md` for the full design and
`plans/2026-07-06-mediatracker-mvp.md` for the milestone roadmap. See
[AGENTS.md](AGENTS.md) for coding conventions and architecture notes.
