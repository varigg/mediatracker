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

// Active reports whether state is a non-frozen lifecycle state eligible
// for background refresh (want_to or in_progress).
func (s State) Active() bool {
	return s == StateWantTo || s == StateInProgress
}

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
