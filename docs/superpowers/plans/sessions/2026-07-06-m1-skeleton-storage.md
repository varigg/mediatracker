# M1 — Skeleton & Storage Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `mediatracker` binary that boots, creates/migrates the SQLite schema in a data dir, loads `config.toml`, and serves a health endpoint — plus a store layer with typed CRUD, state-transition enforcement, and the filter/sort query builder.

**Architecture:** Go module with `cmd/mediatracker`, `internal/config`, `internal/store`, `internal/server`. Embedded sequential SQL migrations tracked in `schema_version`; SQLite in WAL mode via `modernc.org/sqlite` (CGO-free, keeps the bare-binary deploy story). Store methods are short per-call transactions; the query builder maps URL params to SQL + args.

**Tech Stack:** Go 1.26, `modernc.org/sqlite`, `github.com/BurntSushi/toml`, `log/slog`, `embed`, stdlib `net/http` (Go 1.22+ method patterns).

## Global Constraints

- API keys live in `config.toml` in the data dir — never env vars, never committed; never read `.env` files.
- Meaningful, high-value tests over coverage maximization.
- System failures fail loudly; user-input failures return typed errors the HTTP layer can map to 4xx.
- All state (db, covers, catalogs, config) under one data dir (default `~/.local/share/mediatracker/`, flag-overridable).
- Conventional Commits; **no AI attribution anywhere** (no Co-Authored-By, no generated-with footers).
- Storage tests run against real SQLite temp files — no mocks.
- Module path: `github.com/varigg/mediatracker`.

## Execution Notes

- Work in worktree `.worktrees/m1-skeleton-storage` on branch `m1-skeleton-storage` (per user global CLAUDE.md).
- Before every commit: `gofmt -l .` must print nothing and `go vet ./...` must pass.
- Timestamps are stored and surfaced as SQLite text (`YYYY-MM-DD HH:MM:SS`); model fields use `string`/`*string`, not `time.Time`. `completed_at` is a bare date `YYYY-MM-DD`.
- **Execution deviation:** timestamp columns are declared `TEXT` (not `DATE`/`DATETIME`) — modernc.org/sqlite converts `DATE`/`DATETIME`-declared columns to `time.Time` at scan time, which broke string round-trips. `CURRENT_TIMESTAMP`/`DATE('now')` storage semantics are unchanged.

### Decision: transition legality matrix (needs sign-off)

The spec mandates a pure `CanTransition` function but does not enumerate the matrix. This plan uses:

| from \ to | want_to | in_progress | done | abandoned |
|---|---|---|---|---|
| want_to | ✗ | ✓ | ✓ | ✓ |
| in_progress | ✓ | ✓* | ✓ | ✓ |
| done | ✗ | ✓ | ✗ | ✗ |
| abandoned | ✓ | ✓ | ✗ | ✗ |

\* self-transitions are all illegal (the `in_progress→in_progress` cell is ✗). Rationale: forward moves may skip `in_progress` (short media); `in_progress→want_to` is an undo; `done→in_progress` is a re-consume; `abandoned` can be revived; terminal→terminal moves are nonsense. Entering `done`/`abandoned` stamps `completed_at`; leaving a terminal state clears `verdict` and `completed_at`.

### Decision: no FK on `availability.service_slug`

TMDB watch-providers returns many services beyond the seeded US catalog. Availability rows keep provider-sourced slugs without an FK; the available-to-me filter inner-joins `services`, so unknown slugs simply never count as "mine". (M3 may insert new `services` rows on discovery.)

---

### Task 1: Module scaffold + config loading

**Files:**
- Create: `go.mod` (via `go mod init`), `.gitignore`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Load(dataDir string) (Config, error)`, `config.Default() Config`; `Config{ListenAddr string; LogLevel string; RefreshInterval Duration; Providers Providers}`; `Duration{time.Duration}`; `Providers{TMDBKey, OMDBKey, IGDBClientID, IGDBClientSecret, HardcoverKey, SteamKey, SteamID string}`.

- [ ] **Step 1: Scaffold the module**

```bash
go mod init github.com/varigg/mediatracker
go get github.com/BurntSushi/toml@latest
```

Create `.gitignore`:

```
mediatracker
*.db
*.db-wal
*.db-shm
```

- [ ] **Step 2: Write the failing test**

`internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, ":8080")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.RefreshInterval.Duration != 7*24*time.Hour {
		t.Errorf("RefreshInterval = %v, want %v", cfg.RefreshInterval.Duration, 7*24*time.Hour)
	}
}

func TestLoadReadsValues(t *testing.T) {
	dir := t.TempDir()
	data := `
listen_addr = ":9090"
log_level = "debug"
refresh_interval = "24h"

[providers]
tmdb_key = "tmdb-secret"
igdb_client_id = "igdb-id"
igdb_client_secret = "igdb-secret"
steam_id = "7656119"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, ":9090")
	}
	if cfg.RefreshInterval.Duration != 24*time.Hour {
		t.Errorf("RefreshInterval = %v, want 24h", cfg.RefreshInterval.Duration)
	}
	if cfg.Providers.TMDBKey != "tmdb-secret" {
		t.Errorf("TMDBKey = %q, want %q", cfg.Providers.TMDBKey, "tmdb-secret")
	}
	if cfg.Providers.IGDBClientSecret != "igdb-secret" {
		t.Errorf("IGDBClientSecret = %q, want %q", cfg.Providers.IGDBClientSecret, "igdb-secret")
	}
	if cfg.Providers.SteamID != "7656119" {
		t.Errorf("SteamID = %q, want %q", cfg.Providers.SteamID, "7656119")
	}
}

func TestLoadMalformedFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("listen_addr = [broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load: want error for malformed config, got nil")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL (build error: `Load` undefined)

- [ ] **Step 4: Implement config**

`internal/config/config.go`:

```go
// Package config loads mediatracker's config.toml from the data dir.
// API keys live here — never in env vars.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	ListenAddr      string    `toml:"listen_addr"`
	LogLevel        string    `toml:"log_level"`
	RefreshInterval Duration  `toml:"refresh_interval"`
	Providers       Providers `toml:"providers"`
}

type Providers struct {
	TMDBKey          string `toml:"tmdb_key"`
	OMDBKey          string `toml:"omdb_key"`
	IGDBClientID     string `toml:"igdb_client_id"`
	IGDBClientSecret string `toml:"igdb_client_secret"`
	HardcoverKey     string `toml:"hardcover_key"`
	SteamKey         string `toml:"steam_key"`
	SteamID          string `toml:"steam_id"`
}

// Duration wraps time.Duration so TOML values like "24h" parse.
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func Default() Config {
	return Config{
		ListenAddr:      ":8080",
		LogLevel:        "info",
		RefreshInterval: Duration{7 * 24 * time.Hour},
	}
}

// Load reads config.toml from dataDir. A missing file yields defaults;
// an unreadable or malformed file is an error.
func Load(dataDir string) (Config, error) {
	cfg := Default()
	path := filepath.Join(dataDir, "config.toml")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS (3 tests)

- [ ] **Step 6: Commit**

```bash
gofmt -l . && go vet ./...
git add go.mod go.sum .gitignore internal/config/
git commit -m "feat: scaffold module and config.toml loading"
```

---

### Task 2: Store open, migrations, schema v1

**Files:**
- Create: `internal/store/store.go`
- Create: `internal/store/migrate.go`
- Create: `internal/store/migrations/0001_init.sql`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces: `store.Open(ctx context.Context, path string) (*Store, error)`; `(*Store).Close() error`; `(*Store).Ping(ctx context.Context) error`. `Store` holds an unexported `db *sql.DB` that same-package files use. Test helper `newTestStore(t *testing.T) *Store` reused by all later store tests.

- [ ] **Step 1: Write the failing test**

`internal/store/store_test.go`:

```go
package store

import (
	"context"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenMigratesFromEmpty(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != 1 {
		t.Errorf("schema version = %d, want 1", version)
	}

	for _, table := range []string{"media_items", "ratings", "availability", "services", "settings"} {
		var name string
		err := s.db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}

	var mode string
	if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}

	var services int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM services`).Scan(&services); err != nil {
		t.Fatalf("count services: %v", err)
	}
	if services == 0 {
		t.Error("services table not seeded")
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	s1, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1.Close()

	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()

	var count int
	if err := s2.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_version`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("schema_version rows = %d, want 1 (migration re-applied?)", count)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -v`
Expected: FAIL (build error: `Open` undefined)

- [ ] **Step 3: Add the sqlite driver**

```bash
go get modernc.org/sqlite@latest
```

- [ ] **Step 4: Write schema v1**

`internal/store/migrations/0001_init.sql`:

```sql
CREATE TABLE media_items (
    id            INTEGER PRIMARY KEY,
    media_type    TEXT NOT NULL CHECK (media_type IN ('movie','tv','book','game')),
    title         TEXT NOT NULL,
    state         TEXT NOT NULL DEFAULT 'want_to'
                  CHECK (state IN ('want_to','in_progress','done','abandoned')),
    verdict       TEXT CHECK (verdict IN ('liked','ok','disliked')),
    completed_at  DATE,
    notes         TEXT NOT NULL DEFAULT '',
    release_year  INTEGER,
    genres        TEXT NOT NULL DEFAULT '[]',
    cover_path    TEXT,
    provider      TEXT NOT NULL,
    provider_id   TEXT NOT NULL,
    metadata      TEXT NOT NULL DEFAULT '{}',
    added_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    refreshed_at  DATETIME,
    UNIQUE (provider, provider_id)
);

CREATE TABLE ratings (
    item_id  INTEGER NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    source   TEXT NOT NULL,
    score    INTEGER NOT NULL CHECK (score BETWEEN 0 AND 100),
    display  TEXT NOT NULL,
    url      TEXT,
    UNIQUE (item_id, source)
);

CREATE TABLE services (
    slug       TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    media_kind TEXT NOT NULL CHECK (media_kind IN ('video','game','book')),
    subscribed INTEGER NOT NULL DEFAULT 0
);

-- service_slug has no FK: availability is provider-sourced and may name
-- services outside the seeded catalog; the available-to-me filter joins
-- services, so unknown slugs never count as subscribed.
CREATE TABLE availability (
    item_id       INTEGER NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    service_slug  TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('stream','subscription','owned')),
    url           TEXT,
    first_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    fetched_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (item_id, service_slug, kind)
);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO services (slug, name, media_kind) VALUES
    ('netflix',        'Netflix',          'video'),
    ('prime_video',    'Prime Video',      'video'),
    ('disney_plus',    'Disney+',          'video'),
    ('hulu',           'Hulu',             'video'),
    ('max',            'Max',              'video'),
    ('apple_tv_plus',  'Apple TV+',        'video'),
    ('paramount_plus', 'Paramount+',       'video'),
    ('peacock',        'Peacock',          'video'),
    ('game_pass',      'Game Pass',        'game'),
    ('ps_plus',        'PlayStation Plus', 'game'),
    ('steam',          'Steam',            'game');
```

- [ ] **Step 5: Implement Open and the migration runner**

`internal/store/store.go`:

```go
// Package store is the SQLite persistence layer: schema migrations, typed
// CRUD, lifecycle enforcement, and the list query builder.
package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

// Open opens (creating if absent) the SQLite database at path, enables WAL
// and foreign keys on every pooled connection, and applies pending
// migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
```

`internal/store/migrate.go`:

```go
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrate applies embedded migrations newer than the recorded version, each
// in its own transaction, forward-only.
func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (
		version    INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&current); err != nil {
		return fmt.Errorf("read schema_version: %w", err)
	}

	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		if version <= current {
			continue
		}
		src, err := migrationsFS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(src)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_version (version) VALUES (?)`, version); err != nil {
			tx.Rollback()
			return fmt.Errorf("record %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit %s: %w", name, err)
		}
	}
	return nil
}

// migrationVersion extracts N from "migrations/000N_name.sql".
func migrationVersion(name string) (int, error) {
	base := strings.TrimPrefix(name, "migrations/")
	prefix, _, ok := strings.Cut(base, "_")
	if !ok {
		return 0, fmt.Errorf("migration %s: name must be NNNN_description.sql", name)
	}
	v, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("migration %s: bad version prefix: %w", name, err)
	}
	return v, nil
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS (2 tests)

- [ ] **Step 7: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/store/ go.mod go.sum
git commit -m "feat: add sqlite store with embedded schema v1 migrations"
```

---

### Task 3: Lifecycle transition legality

**Files:**
- Create: `internal/store/models.go`
- Create: `internal/store/transitions.go`
- Test: `internal/store/transitions_test.go`

**Interfaces:**
- Produces: types `MediaType`, `State`, `Verdict` (string kinds) with constants `TypeMovie/TypeTV/TypeBook/TypeGame`, `StateWantTo/StateInProgress/StateDone/StateAbandoned`, `VerdictLiked/VerdictOK/VerdictDisliked`; structs `MediaItem`, `Rating`, `Availability`, `Service`; funcs `CanTransition(from, to State) bool` and `LegalTransitions(from State) []State`.

- [ ] **Step 1: Write the failing test**

`internal/store/transitions_test.go`:

```go
package store

import (
	"reflect"
	"testing"
)

// Exhaustive 4x4 matrix (plus unknown-state guards). Kept as an explicit
// table so a legality change is a visible diff here, not an accident.
func TestCanTransition(t *testing.T) {
	cases := []struct {
		from, to State
		want     bool
	}{
		{StateWantTo, StateWantTo, false},
		{StateWantTo, StateInProgress, true},
		{StateWantTo, StateDone, true},
		{StateWantTo, StateAbandoned, true},

		{StateInProgress, StateWantTo, true},
		{StateInProgress, StateInProgress, false},
		{StateInProgress, StateDone, true},
		{StateInProgress, StateAbandoned, true},

		{StateDone, StateWantTo, false},
		{StateDone, StateInProgress, true},
		{StateDone, StateDone, false},
		{StateDone, StateAbandoned, false},

		{StateAbandoned, StateWantTo, true},
		{StateAbandoned, StateInProgress, true},
		{StateAbandoned, StateDone, false},
		{StateAbandoned, StateAbandoned, false},

		{State("bogus"), StateDone, false},
		{StateWantTo, State("bogus"), false},
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestLegalTransitions(t *testing.T) {
	got := LegalTransitions(StateDone)
	want := []State{StateInProgress}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LegalTransitions(done) = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run Transition -v`
Expected: FAIL (build error: `CanTransition` undefined)

- [ ] **Step 3: Implement models and transitions**

`internal/store/models.go`:

```go
package store

import "encoding/json"

type MediaType string

const (
	TypeMovie MediaType = "movie"
	TypeTV    MediaType = "tv"
	TypeBook  MediaType = "book"
	TypeGame  MediaType = "game"
)

type State string

const (
	StateWantTo     State = "want_to"
	StateInProgress State = "in_progress"
	StateDone       State = "done"
	StateAbandoned  State = "abandoned"
)

type Verdict string

const (
	VerdictLiked    Verdict = "liked"
	VerdictOK       Verdict = "ok"
	VerdictDisliked Verdict = "disliked"
)

// MediaItem mirrors a media_items row. Timestamps are SQLite text
// ("YYYY-MM-DD HH:MM:SS"); CompletedAt is a bare date ("YYYY-MM-DD").
type MediaItem struct {
	ID          int64
	MediaType   MediaType
	Title       string
	State       State
	Verdict     *Verdict
	CompletedAt *string
	Notes       string
	ReleaseYear *int
	Genres      []string
	CoverPath   *string
	Provider    string
	ProviderID  string
	Metadata    json.RawMessage
	AddedAt     string
	RefreshedAt *string
}

type Rating struct {
	ItemID  int64
	Source  string
	Score   int // normalized 0–100
	Display string
	URL     *string
}

type Availability struct {
	ItemID      int64
	ServiceSlug string
	Kind        string // stream | subscription | owned
	URL         *string
	FirstSeenAt string
	FetchedAt   string
}

type Service struct {
	Slug       string
	Name       string
	MediaKind  string // video | game | book
	Subscribed bool
}
```

`internal/store/transitions.go`:

```go
package store

// legality: forward moves may skip in_progress; in_progress→want_to is an
// undo; done→in_progress is a re-consume; abandoned can be revived;
// self- and terminal→terminal transitions are illegal.
var legalTransitions = map[State][]State{
	StateWantTo:     {StateInProgress, StateDone, StateAbandoned},
	StateInProgress: {StateWantTo, StateDone, StateAbandoned},
	StateDone:       {StateInProgress},
	StateAbandoned:  {StateWantTo, StateInProgress},
}

// CanTransition reports whether a lifecycle move from → to is legal.
func CanTransition(from, to State) bool {
	for _, s := range legalTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// LegalTransitions lists the states reachable from the given state, in a
// stable order for rendering.
func LegalTransitions(from State) []State {
	return legalTransitions[from]
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/store/models.go internal/store/transitions.go internal/store/transitions_test.go
git commit -m "feat: add domain types and lifecycle transition legality"
```

---

### Task 4: Item CRUD with transition enforcement

**Files:**
- Create: `internal/store/items.go`
- Test: `internal/store/items_test.go`

**Interfaces:**
- Consumes: `newTestStore`, `CanTransition`, models from Tasks 2–3.
- Produces: `NewItem{MediaType MediaType; Title string; ReleaseYear *int; Genres []string; Provider string; ProviderID string; Metadata json.RawMessage}`; `(*Store).CreateItem(ctx, NewItem) (*MediaItem, bool, error)` (bool = newly created; false = pre-existing row returned); `(*Store).GetItem(ctx, id int64) (*MediaItem, error)`; `(*Store).UpdateState(ctx, id int64, to State) error`; `(*Store).UpdateReview(ctx, id int64, v Verdict, completedAt string) error`; `(*Store).UpdateNotes(ctx, id int64, notes string) error`; sentinel errors `ErrNotFound`, `ErrIllegalTransition`, `ErrNotTerminal`; unexported `scanItem` + `selectItem` reused by Task 6.

- [ ] **Step 1: Write the failing test**

`internal/store/items_test.go`:

```go
package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func intPtr(i int) *int { return &i }

func mustCreate(t *testing.T, s *Store, n NewItem) *MediaItem {
	t.Helper()
	it, _, err := s.CreateItem(context.Background(), n)
	if err != nil {
		t.Fatalf("CreateItem(%s): %v", n.Title, err)
	}
	return it
}

func TestCreateAndGetItem(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	it, created, err := s.CreateItem(ctx, NewItem{
		MediaType:   TypeMovie,
		Title:       "Heat",
		ReleaseYear: intPtr(1995),
		Genres:      []string{"Crime", "Thriller"},
		Provider:    "tmdb",
		ProviderID:  "949",
		Metadata:    json.RawMessage(`{"tmdb_id":949}`),
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if !created {
		t.Error("created = false, want true")
	}
	if it.State != StateWantTo {
		t.Errorf("State = %q, want want_to", it.State)
	}

	got, err := s.GetItem(ctx, it.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.Title != "Heat" || got.Provider != "tmdb" || got.ProviderID != "949" {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if !reflect.DeepEqual(got.Genres, []string{"Crime", "Thriller"}) {
		t.Errorf("Genres = %v", got.Genres)
	}
	if got.ReleaseYear == nil || *got.ReleaseYear != 1995 {
		t.Errorf("ReleaseYear = %v, want 1995", got.ReleaseYear)
	}
	if got.AddedAt == "" {
		t.Error("AddedAt empty")
	}
	if got.Notes != "" || got.Verdict != nil || got.CompletedAt != nil {
		t.Errorf("fresh item has review fields set: %+v", got)
	}
}

func TestCreateItemIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	first := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})

	again, created, err := s.CreateItem(ctx, NewItem{
		MediaType: TypeMovie, Title: "Heat (retitled)", Provider: "tmdb", ProviderID: "949",
	})
	if err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if created {
		t.Error("created = true on duplicate, want false")
	}
	if again.ID != first.ID {
		t.Errorf("duplicate returned ID %d, want existing %d", again.ID, first.ID)
	}
	if again.Title != "Heat" {
		t.Errorf("duplicate overwrote title: %q", again.Title)
	}

	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_items`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("row count = %d, want 1", count)
	}
}

func TestGetItemNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetItem(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateStateLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeGame, Title: "Hades", Provider: "igdb", ProviderID: "113112"})

	if err := s.UpdateState(ctx, it.ID, StateInProgress); err != nil {
		t.Fatalf("want_to→in_progress: %v", err)
	}
	if err := s.UpdateState(ctx, it.ID, StateDone); err != nil {
		t.Fatalf("in_progress→done: %v", err)
	}
	got, _ := s.GetItem(ctx, it.ID)
	if got.State != StateDone {
		t.Errorf("State = %q, want done", got.State)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt not stamped on done")
	}

	// Reopening clears the review fields.
	if err := s.UpdateReview(ctx, it.ID, VerdictLiked, "2026-07-01"); err != nil {
		t.Fatalf("UpdateReview: %v", err)
	}
	if err := s.UpdateState(ctx, it.ID, StateInProgress); err != nil {
		t.Fatalf("done→in_progress: %v", err)
	}
	got, _ = s.GetItem(ctx, it.ID)
	if got.Verdict != nil || got.CompletedAt != nil {
		t.Errorf("reopen kept review fields: verdict=%v completed=%v", got.Verdict, got.CompletedAt)
	}
}

func TestUpdateStateIllegal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeBook, Title: "Dune", Provider: "openlibrary", ProviderID: "OL893415W"})

	if err := s.UpdateState(ctx, it.ID, StateDone); err != nil {
		t.Fatalf("want_to→done: %v", err)
	}
	if err := s.UpdateState(ctx, it.ID, StateAbandoned); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("done→abandoned err = %v, want ErrIllegalTransition", err)
	}
	if err := s.UpdateState(ctx, 999, StateDone); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing item err = %v, want ErrNotFound", err)
	}
}

func TestUpdateReviewRequiresTerminalState(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Ran", Provider: "tmdb", ProviderID: "11645"})

	if err := s.UpdateReview(ctx, it.ID, VerdictLiked, "2026-07-01"); !errors.Is(err, ErrNotTerminal) {
		t.Errorf("err = %v, want ErrNotTerminal", err)
	}
	if err := s.UpdateReview(ctx, 999, VerdictLiked, "2026-07-01"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing item err = %v, want ErrNotFound", err)
	}

	if err := s.UpdateState(ctx, it.ID, StateDone); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateReview(ctx, it.ID, VerdictOK, "2026-07-02"); err != nil {
		t.Fatalf("UpdateReview on done item: %v", err)
	}
	got, _ := s.GetItem(ctx, it.ID)
	if got.Verdict == nil || *got.Verdict != VerdictOK {
		t.Errorf("Verdict = %v, want ok", got.Verdict)
	}
	if got.CompletedAt == nil || *got.CompletedAt != "2026-07-02" {
		t.Errorf("CompletedAt = %v, want 2026-07-02", got.CompletedAt)
	}
}

func TestUpdateNotes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeTV, Title: "Severance", Provider: "tmdb", ProviderID: "95396"})

	if err := s.UpdateNotes(ctx, it.ID, "## S1\nGreat."); err != nil {
		t.Fatalf("UpdateNotes: %v", err)
	}
	got, _ := s.GetItem(ctx, it.ID)
	if got.Notes != "## S1\nGreat." {
		t.Errorf("Notes = %q", got.Notes)
	}
	if err := s.UpdateNotes(ctx, 999, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing item err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -v`
Expected: FAIL (build error: `NewItem`, `CreateItem` undefined)

- [ ] **Step 3: Implement item CRUD**

`internal/store/items.go`:

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrIllegalTransition = errors.New("illegal state transition")
	ErrNotTerminal       = errors.New("item not in a terminal state")
)

type NewItem struct {
	MediaType   MediaType
	Title       string
	ReleaseYear *int
	Genres      []string
	Provider    string
	ProviderID  string
	Metadata    json.RawMessage
}

const selectItem = `SELECT id, media_type, title, state, verdict, completed_at,
	notes, release_year, genres, cover_path, provider, provider_id, metadata,
	added_at, refreshed_at FROM media_items`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanItem(r rowScanner) (*MediaItem, error) {
	var it MediaItem
	var genres, metadata string
	err := r.Scan(&it.ID, &it.MediaType, &it.Title, &it.State, &it.Verdict,
		&it.CompletedAt, &it.Notes, &it.ReleaseYear, &genres, &it.CoverPath,
		&it.Provider, &it.ProviderID, &metadata, &it.AddedAt, &it.RefreshedAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(genres), &it.Genres); err != nil {
		return nil, fmt.Errorf("item %d: bad genres JSON: %w", it.ID, err)
	}
	it.Metadata = json.RawMessage(metadata)
	return &it, nil
}

// CreateItem inserts a new item in state want_to. If (provider,
// provider_id) already exists, the existing row is returned unmodified and
// the bool is false — re-adding surfaces the existing item.
func (s *Store) CreateItem(ctx context.Context, n NewItem) (*MediaItem, bool, error) {
	genres := []byte("[]")
	if n.Genres != nil {
		var err error
		if genres, err = json.Marshal(n.Genres); err != nil {
			return nil, false, err
		}
	}
	metadata := "{}"
	if len(n.Metadata) > 0 {
		metadata = string(n.Metadata)
	}

	res, err := s.db.ExecContext(ctx, `INSERT INTO media_items
		(media_type, title, release_year, genres, provider, provider_id, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (provider, provider_id) DO NOTHING`,
		n.MediaType, n.Title, n.ReleaseYear, string(genres), n.Provider, n.ProviderID, metadata)
	if err != nil {
		return nil, false, err
	}
	if rows, err := res.RowsAffected(); err != nil {
		return nil, false, err
	} else if rows == 1 {
		id, err := res.LastInsertId()
		if err != nil {
			return nil, false, err
		}
		it, err := s.GetItem(ctx, id)
		return it, true, err
	}

	it, err := scanItem(s.db.QueryRowContext(ctx,
		selectItem+` WHERE provider = ? AND provider_id = ?`, n.Provider, n.ProviderID))
	return it, false, err
}

func (s *Store) GetItem(ctx context.Context, id int64) (*MediaItem, error) {
	it, err := scanItem(s.db.QueryRowContext(ctx, selectItem+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return it, err
}

// UpdateState moves an item to a new lifecycle state, enforcing
// CanTransition. Entering done/abandoned stamps completed_at with today;
// entering a non-terminal state clears verdict and completed_at.
func (s *Store) UpdateState(ctx context.Context, id int64, to State) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var from State
	err = tx.QueryRowContext(ctx, `SELECT state FROM media_items WHERE id = ?`, id).Scan(&from)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: %s → %s", ErrIllegalTransition, from, to)
	}

	if to == StateDone || to == StateAbandoned {
		_, err = tx.ExecContext(ctx,
			`UPDATE media_items SET state = ?, completed_at = DATE('now') WHERE id = ?`, to, id)
	} else {
		_, err = tx.ExecContext(ctx,
			`UPDATE media_items SET state = ?, verdict = NULL, completed_at = NULL WHERE id = ?`, to, id)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateReview sets verdict and completion date; legal only in done or
// abandoned states.
func (s *Store) UpdateReview(ctx context.Context, id int64, v Verdict, completedAt string) error {
	switch v {
	case VerdictLiked, VerdictOK, VerdictDisliked:
	default:
		return fmt.Errorf("invalid verdict %q", v)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE media_items SET verdict = ?, completed_at = ?
		WHERE id = ? AND state IN ('done', 'abandoned')`, v, completedAt, id)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err != nil {
		return err
	} else if rows == 1 {
		return nil
	}
	if _, err := s.GetItem(ctx, id); err != nil {
		return err // ErrNotFound
	}
	return ErrNotTerminal
}

func (s *Store) UpdateNotes(ctx context.Context, id int64, notes string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE media_items SET notes = ? WHERE id = ?`, notes, id)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err != nil {
		return err
	} else if rows == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/store/items.go internal/store/items_test.go
git commit -m "feat: add item CRUD with idempotent add and transition enforcement"
```

---

### Task 5: Ratings, availability, and service subscriptions

**Files:**
- Create: `internal/store/enrichment.go`
- Test: `internal/store/enrichment_test.go`

**Interfaces:**
- Consumes: `newTestStore`, `mustCreate`, models.
- Produces: `(*Store).ReplaceRatings(ctx, itemID int64, ratings []Rating) error`; `(*Store).GetRatings(ctx, itemID int64) ([]Rating, error)` (ordered by source); `(*Store).UpsertAvailability(ctx, itemID int64, rows []Availability) error` (preserves `first_seen_at`, bumps `fetched_at`); `(*Store).GetAvailability(ctx, itemID int64) ([]Availability, error)` (ordered by service_slug, kind); `(*Store).ListServices(ctx) ([]Service, error)`; `(*Store).SetServiceSubscribed(ctx, slug string, subscribed bool) error`.

- [ ] **Step 1: Write the failing test**

`internal/store/enrichment_test.go`:

```go
package store

import (
	"context"
	"errors"
	"testing"
)

func strPtr(s string) *string { return &s }

func TestReplaceRatings(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})

	first := []Rating{
		{Source: "imdb", Score: 82, Display: "8.2/10", URL: strPtr("https://www.imdb.com/title/tt0113277/")},
		{Source: "rotten_tomatoes", Score: 88, Display: "88%"},
	}
	if err := s.ReplaceRatings(ctx, it.ID, first); err != nil {
		t.Fatalf("ReplaceRatings: %v", err)
	}

	second := []Rating{{Source: "imdb", Score: 83, Display: "8.3/10"}}
	if err := s.ReplaceRatings(ctx, it.ID, second); err != nil {
		t.Fatalf("second ReplaceRatings: %v", err)
	}

	got, err := s.GetRatings(ctx, it.ID)
	if err != nil {
		t.Fatalf("GetRatings: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ratings = %d rows, want 1 (replace, not merge)", len(got))
	}
	if got[0].Source != "imdb" || got[0].Score != 83 || got[0].Display != "8.3/10" {
		t.Errorf("rating = %+v", got[0])
	}
}

func TestUpsertAvailabilityPreservesFirstSeen(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	it := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Heat", Provider: "tmdb", ProviderID: "949"})

	if err := s.UpsertAvailability(ctx, it.ID, []Availability{
		{ServiceSlug: "netflix", Kind: "subscription", URL: strPtr("https://www.netflix.com/title/1")},
	}); err != nil {
		t.Fatalf("UpsertAvailability: %v", err)
	}

	// Backdate so a preserved first_seen_at is distinguishable from a
	// same-second rewrite.
	if _, err := s.db.ExecContext(ctx, `UPDATE availability
		SET first_seen_at = '2000-01-01 00:00:00', fetched_at = '2000-01-01 00:00:00'
		WHERE item_id = ?`, it.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.UpsertAvailability(ctx, it.ID, []Availability{
		{ServiceSlug: "netflix", Kind: "subscription", URL: strPtr("https://www.netflix.com/title/2")},
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	rows, err := s.GetAvailability(ctx, it.ID)
	if err != nil {
		t.Fatalf("GetAvailability: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("availability = %d rows, want 1", len(rows))
	}
	if rows[0].FirstSeenAt != "2000-01-01 00:00:00" {
		t.Errorf("first_seen_at = %q, want preserved 2000-01-01 00:00:00", rows[0].FirstSeenAt)
	}
	if rows[0].FetchedAt == "2000-01-01 00:00:00" {
		t.Error("fetched_at not bumped on upsert")
	}
	if rows[0].URL == nil || *rows[0].URL != "https://www.netflix.com/title/2" {
		t.Errorf("url = %v, want updated title/2", rows[0].URL)
	}
}

func TestSetServiceSubscribed(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.SetServiceSubscribed(ctx, "netflix", true); err != nil {
		t.Fatalf("SetServiceSubscribed: %v", err)
	}
	services, err := s.ListServices(ctx)
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	found := false
	for _, svc := range services {
		if svc.Slug == "netflix" {
			found = true
			if !svc.Subscribed {
				t.Error("netflix not marked subscribed")
			}
		} else if svc.Subscribed {
			t.Errorf("%s unexpectedly subscribed", svc.Slug)
		}
	}
	if !found {
		t.Fatal("netflix missing from seeded services")
	}

	if err := s.SetServiceSubscribed(ctx, "no_such_service", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown slug err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -v`
Expected: FAIL (build error: `ReplaceRatings` undefined)

- [ ] **Step 3: Implement enrichment writes**

`internal/store/enrichment.go`:

```go
package store

import "context"

// ReplaceRatings replaces all rating rows for an item atomically.
func (s *Store) ReplaceRatings(ctx context.Context, itemID int64, ratings []Rating) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM ratings WHERE item_id = ?`, itemID); err != nil {
		return err
	}
	for _, r := range ratings {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ratings
			(item_id, source, score, display, url) VALUES (?, ?, ?, ?, ?)`,
			itemID, r.Source, r.Score, r.Display, r.URL); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetRatings(ctx context.Context, itemID int64) ([]Rating, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, source, score, display, url
		FROM ratings WHERE item_id = ? ORDER BY source`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Rating
	for rows.Next() {
		var r Rating
		if err := rows.Scan(&r.ItemID, &r.Source, &r.Score, &r.Display, &r.URL); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertAvailability inserts or refreshes availability rows. Existing rows
// keep first_seen_at (it powers the "newly available" diff) and get
// fetched_at bumped.
func (s *Store) UpsertAvailability(ctx context.Context, itemID int64, avail []Availability) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, a := range avail {
		if _, err := tx.ExecContext(ctx, `INSERT INTO availability
			(item_id, service_slug, kind, url) VALUES (?, ?, ?, ?)
			ON CONFLICT (item_id, service_slug, kind)
			DO UPDATE SET url = excluded.url, fetched_at = CURRENT_TIMESTAMP`,
			itemID, a.ServiceSlug, a.Kind, a.URL); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetAvailability(ctx context.Context, itemID int64) ([]Availability, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, service_slug, kind, url,
		first_seen_at, fetched_at FROM availability WHERE item_id = ?
		ORDER BY service_slug, kind`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Availability
	for rows.Next() {
		var a Availability
		if err := rows.Scan(&a.ItemID, &a.ServiceSlug, &a.Kind, &a.URL,
			&a.FirstSeenAt, &a.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ListServices(ctx context.Context) ([]Service, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT slug, name, media_kind, subscribed FROM services ORDER BY media_kind, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Service
	for rows.Next() {
		var svc Service
		if err := rows.Scan(&svc.Slug, &svc.Name, &svc.MediaKind, &svc.Subscribed); err != nil {
			return nil, err
		}
		out = append(out, svc)
	}
	return out, rows.Err()
}

func (s *Store) SetServiceSubscribed(ctx context.Context, slug string, subscribed bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE services SET subscribed = ? WHERE slug = ?`, subscribed, slug)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err != nil {
		return err
	} else if rows == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/store/enrichment.go internal/store/enrichment_test.go
git commit -m "feat: add ratings, availability, and service subscription writes"
```

---

### Task 6: Filter/sort query builder + ListItems

**Files:**
- Create: `internal/store/query.go`
- Test: `internal/store/query_test.go`

**Interfaces:**
- Consumes: `scanItem`, models, `UpsertAvailability`, `ReplaceRatings`, `SetServiceSubscribed`.
- Produces: `BuildListQuery(v url.Values) (string, []any, error)`; `(*Store).ListItems(ctx, v url.Values) ([]MediaItem, error)`. Params: `state` (one lifecycle state), `type` (repeatable media type), `genre` (exact string), `available=1` (subscribed service or owned), `sort` = `added` (default) | `year` | `rating` | `title`. Invalid values return an error the HTTP layer maps to 400.

- [ ] **Step 1: Write the failing test**

`internal/store/query_test.go`:

```go
package store

import (
	"context"
	"net/url"
	"testing"
)

// seedListFixture creates four items with distinct types, states, genres,
// ratings, and availability. added-order (and thus id-order) is Alpha,
// Bravo, Charlie, Delta.
func seedListFixture(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()

	alpha := mustCreate(t, s, NewItem{MediaType: TypeMovie, Title: "Alpha",
		ReleaseYear: intPtr(2001), Genres: []string{"Drama"}, Provider: "tmdb", ProviderID: "1"})
	bravo := mustCreate(t, s, NewItem{MediaType: TypeTV, Title: "Bravo",
		ReleaseYear: intPtr(1999), Genres: []string{"Comedy"}, Provider: "tmdb", ProviderID: "2"})
	charlie := mustCreate(t, s, NewItem{MediaType: TypeBook, Title: "Charlie",
		ReleaseYear: intPtr(2010), Genres: []string{"Drama"}, Provider: "openlibrary", ProviderID: "3"})
	delta := mustCreate(t, s, NewItem{MediaType: TypeGame, Title: "Delta",
		ReleaseYear: intPtr(2020), Provider: "igdb", ProviderID: "4"})

	if err := s.UpdateState(ctx, charlie.ID, StateInProgress); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceRatings(ctx, alpha.ID, []Rating{{Source: "imdb", Score: 90, Display: "9.0/10"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceRatings(ctx, bravo.ID, []Rating{{Source: "imdb", Score: 70, Display: "7.0/10"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAvailability(ctx, bravo.ID, []Availability{
		{ServiceSlug: "netflix", Kind: "subscription"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAvailability(ctx, delta.ID, []Availability{
		{ServiceSlug: "steam", Kind: "owned", URL: strPtr("https://store.steampowered.com/app/4")},
	}); err != nil {
		t.Fatal(err)
	}
}

func titles(items []MediaItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out
}

func TestListItems(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedListFixture(t, s)
	if err := s.SetServiceSubscribed(ctx, "netflix", true); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		params url.Values
		want   []string // titles in expected order
	}{
		{"no filters, default added-desc sort", url.Values{},
			[]string{"Delta", "Charlie", "Bravo", "Alpha"}},
		{"state filter", url.Values{"state": {"want_to"}},
			[]string{"Delta", "Bravo", "Alpha"}},
		{"movies-tv tab types", url.Values{"type": {"movie", "tv"}},
			[]string{"Bravo", "Alpha"}},
		{"genre filter", url.Values{"genre": {"Drama"}},
			[]string{"Charlie", "Alpha"}},
		{"available to me: subscription + owned", url.Values{"available": {"1"}},
			[]string{"Delta", "Bravo"}},
		{"sort title", url.Values{"sort": {"title"}},
			[]string{"Alpha", "Bravo", "Charlie", "Delta"}},
		{"sort year desc", url.Values{"sort": {"year"}},
			[]string{"Delta", "Charlie", "Alpha", "Bravo"}},
		{"sort rating desc, unrated last by title", url.Values{"sort": {"rating"}},
			[]string{"Alpha", "Bravo", "Charlie", "Delta"}},
		{"combined state+type+sort", url.Values{"state": {"want_to"}, "type": {"movie", "tv"}, "sort": {"title"}},
			[]string{"Alpha", "Bravo"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items, err := s.ListItems(ctx, c.params)
			if err != nil {
				t.Fatalf("ListItems(%v): %v", c.params, err)
			}
			got := titles(items)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestListItemsUnsubscribedServiceNotAvailable(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedListFixture(t, s)
	// netflix NOT subscribed: only the owned game counts.
	items, err := s.ListItems(ctx, url.Values{"available": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	got := titles(items)
	if len(got) != 1 || got[0] != "Delta" {
		t.Errorf("got %v, want [Delta]", got)
	}
}

func TestBuildListQueryRejectsInvalidParams(t *testing.T) {
	for _, v := range []url.Values{
		{"state": {"pending"}},
		{"type": {"podcast"}},
		{"sort": {"popularity"}},
	} {
		if _, _, err := BuildListQuery(v); err == nil {
			t.Errorf("BuildListQuery(%v): want error, got nil", v)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -v`
Expected: FAIL (build error: `ListItems`, `BuildListQuery` undefined)

- [ ] **Step 3: Implement the query builder**

`internal/store/query.go`:

```go
package store

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// selectItemList matches selectItem's column order (scanItem depends on
// it) and always joins per-item average rating for the rating sort.
const selectItemList = `SELECT mi.id, mi.media_type, mi.title, mi.state, mi.verdict,
	mi.completed_at, mi.notes, mi.release_year, mi.genres, mi.cover_path,
	mi.provider, mi.provider_id, mi.metadata, mi.added_at, mi.refreshed_at
	FROM media_items mi
	LEFT JOIN (SELECT item_id, AVG(score) AS avg_score FROM ratings GROUP BY item_id) r
	ON r.item_id = mi.id`

// BuildListQuery translates URL query parameters into SQL + args.
//
// Params: state (lifecycle state) · type (media type, repeatable) · genre
// (exact match against the genres JSON array) · available=1 (has a row on
// a subscribed service, or is owned) · sort = added (default) | year |
// rating | title. Unrecognized values are user-input errors (→ 400).
func BuildListQuery(v url.Values) (string, []any, error) {
	var where []string
	var args []any

	if s := v.Get("state"); s != "" {
		switch State(s) {
		case StateWantTo, StateInProgress, StateDone, StateAbandoned:
			where = append(where, "mi.state = ?")
			args = append(args, s)
		default:
			return "", nil, fmt.Errorf("invalid state %q", s)
		}
	}

	if types := v["type"]; len(types) > 0 {
		placeholders := make([]string, len(types))
		for i, t := range types {
			switch MediaType(t) {
			case TypeMovie, TypeTV, TypeBook, TypeGame:
				placeholders[i] = "?"
				args = append(args, t)
			default:
				return "", nil, fmt.Errorf("invalid type %q", t)
			}
		}
		where = append(where, fmt.Sprintf("mi.media_type IN (%s)", strings.Join(placeholders, ", ")))
	}

	if g := v.Get("genre"); g != "" {
		where = append(where, "EXISTS (SELECT 1 FROM json_each(mi.genres) WHERE json_each.value = ?)")
		args = append(args, g)
	}

	if v.Get("available") == "1" {
		where = append(where, `EXISTS (SELECT 1 FROM availability a
			JOIN services s ON s.slug = a.service_slug
			WHERE a.item_id = mi.id AND (s.subscribed = 1 OR a.kind = 'owned'))`)
	}

	var orderBy string
	switch v.Get("sort") {
	case "", "added":
		orderBy = "mi.added_at DESC, mi.id DESC"
	case "year":
		orderBy = "mi.release_year DESC NULLS LAST, mi.title COLLATE NOCASE ASC"
	case "rating":
		orderBy = "r.avg_score DESC NULLS LAST, mi.title COLLATE NOCASE ASC"
	case "title":
		orderBy = "mi.title COLLATE NOCASE ASC"
	default:
		return "", nil, fmt.Errorf("invalid sort %q", v.Get("sort"))
	}

	q := selectItemList
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY " + orderBy
	return q, args, nil
}

// ListItems runs the query BuildListQuery produces for the given params.
func (s *Store) ListItems(ctx context.Context, v url.Values) ([]MediaItem, error) {
	q, args, err := BuildListQuery(v)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []MediaItem
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *it)
	}
	return items, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/store/query.go internal/store/query_test.go
git commit -m "feat: add filter/sort query builder and ListItems"
```

---

### Task 7: HTTP server, health endpoint, main wiring

**Files:**
- Create: `internal/server/server.go`
- Create: `cmd/mediatracker/main.go`
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: `store.Open`, `(*store.Store).Ping/Close`, `config.Load`.
- Produces: `server.New(st server.Store) http.Handler` where `server.Store interface { Ping(context.Context) error }` (widened in M6); binary flags: `-data <dir>` (default `~/.local/share/mediatracker`, honoring `XDG_DATA_HOME`).

- [ ] **Step 1: Write the failing test**

`internal/server/server_test.go`:

```go
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/varigg/mediatracker/internal/store"
)

func TestHealthzOK(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	srv := httptest.NewServer(New(st))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf(`body = %v, want {"status":"ok"}`, body)
	}
}

type failingStore struct{}

func (failingStore) Ping(context.Context) error { return errors.New("db gone") }

func TestHealthzFailsLoudly(t *testing.T) {
	srv := httptest.NewServer(New(failingStore{}))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -v`
Expected: FAIL (build error: package doesn't exist / `New` undefined)

- [ ] **Step 3: Implement server and main**

`internal/server/server.go`:

```go
// Package server is the HTTP layer. In M1 it exposes only the health
// endpoint; M6 adds the full route surface.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

// Store is the subset of the store API the HTTP layer needs.
type Store interface {
	Ping(ctx context.Context) error
}

func New(st Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := st.Ping(r.Context()); err != nil {
			slog.Error("health check failed", "error", err)
			http.Error(w, "database unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	return mux
}
```

`cmd/mediatracker/main.go`:

```go
// Command mediatracker is the self-hosted media tracker server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/varigg/mediatracker/internal/config"
	"github.com/varigg/mediatracker/internal/server"
	"github.com/varigg/mediatracker/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func defaultDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "mediatracker")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "data"
	}
	return filepath.Join(home, ".local", "share", "mediatracker")
}

func run() error {
	dataDir := flag.String("data", defaultDataDir(), "data directory (db, covers, catalogs, config.toml)")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	cfg, err := config.Load(*dataDir)
	if err != nil {
		return err
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		return fmt.Errorf("invalid log_level %q: %w", cfg.LogLevel, err)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, filepath.Join(*dataDir, "app.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	srv := &http.Server{Addr: cfg.ListenAddr, Handler: server.New(st)}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("mediatracker started", "addr", cfg.ListenAddr, "data_dir", *dataDir)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... && go build ./...`
Expected: all packages PASS, clean build

- [ ] **Step 5: Boot smoke test**

```bash
SMOKE_DIR=$(mktemp -d)
go build -o /tmp/mediatracker-smoke ./cmd/mediatracker
printf 'listen_addr = ":18080"\n' > "$SMOKE_DIR/config.toml"
/tmp/mediatracker-smoke -data "$SMOKE_DIR" &
sleep 1
curl -s http://localhost:18080/healthz
kill %1
ls "$SMOKE_DIR"
```

Expected: `{"status":"ok"}` from curl; `app.db` (plus `-wal`/`-shm`) and `config.toml` listed.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/server/ cmd/
git commit -m "feat: add health endpoint and bootable server binary"
```

---

## Self-Review Notes

- M1 scope coverage: module layout → T1/T7; migrations + schema_version → T2; schema v1 incl. seeded services and settings → T2; WAL → T2 (asserted); canTransition → T3; typed CRUD + transition enforcement → T4; availability `first_seen_at` preservation → T5; query builder (state/type/genre/available-to-me, four sorts) → T6; config.toml + health endpoint + boot → T1/T7.
- Milestone key tests all present: migrations-from-empty (T2), exhaustive 4×4 (T3), idempotent re-add (T4), query-builder table-driven (T6), first_seen_at preservation (T5).
- Type consistency verified: `scanItem`/`selectItem` (T4) reused by `selectItemList` (T6) with identical column order; `newTestStore` (T2), `mustCreate`/`intPtr` (T4), `strPtr` (T5) reused downstream; `server.Store` matches `(*store.Store).Ping`.
- Deviations from spec defaults, called out for sign-off: transition matrix (above), no FK on `availability.service_slug` (above), timestamps as strings (M1 simplification, revisit if M4 needs time math).
