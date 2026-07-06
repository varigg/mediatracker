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
