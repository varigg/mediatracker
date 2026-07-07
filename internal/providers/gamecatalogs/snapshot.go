package gamecatalogs

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/varigg/mediatracker/internal/providers/names"
)

// snapshot is the normalized on-disk catalog format. Parsing raw
// upstream payloads happens at fetch time, so unofficial-endpoint shape
// drift is contained in the fetchers.
type snapshot struct {
	FetchedAt string         `json:"fetched_at"` // UTC "2006-01-02 15:04:05"
	Entries   []catalogEntry `json:"entries"`
}

type catalogEntry struct {
	Name string  `json:"name"`
	URL  *string `json:"url,omitempty"`
}

func (p *Provider) snapshotPath(slug string) string {
	return filepath.Join(p.dir, slug+".json")
}

func (p *Provider) saveSnapshot(slug string, entries []catalogEntry) error {
	snap := snapshot{
		FetchedAt: p.now().UTC().Format("2006-01-02 15:04:05"),
		Entries:   entries,
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.snapshotPath(slug) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.snapshotPath(slug))
}

func (p *Provider) loadSnapshot(slug string) (*snapshot, error) {
	data, err := os.ReadFile(p.snapshotPath(slug))
	if err != nil {
		return nil, err
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func buildSet(entries []catalogEntry) *names.Set {
	s := names.NewSet()
	for _, e := range entries {
		s.Add(e.Name, e.URL)
	}
	return s
}

func (p *Provider) setSet(slug string, s *names.Set) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sets[slug] = s
}

// set returns the in-memory lookup for a catalog, lazily loading the
// snapshot from disk (startup before first sync). Missing snapshot ⇒
// nil: no availability facts, not an error.
func (p *Provider) set(slug string) *names.Set {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sets[slug]; ok {
		return s
	}
	snap, err := p.loadSnapshot(slug)
	if err != nil {
		return nil
	}
	s := buildSet(snap.Entries)
	p.sets[slug] = s
	return s
}
