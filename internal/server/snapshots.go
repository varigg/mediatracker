package server

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// catalogSnapshotFetchedAt is the sliver of the on-disk catalog
// snapshot format (internal/providers/gamecatalogs, internal/providers/
// steam) the settings page needs: just fetched_at. Unmarshaling ignores
// the rest of the file (entries/games).
type catalogSnapshotFetchedAt struct {
	FetchedAt string `json:"fetched_at"`
}

// snapshotAge reads {dataDir}/catalogs/{slug}.json and returns its
// fetched_at value verbatim (store.TimeFormat). A missing file or a
// parse failure both degrade to "never" (plan decision 2: snapshot ages
// come straight from the files themselves, no store surface) — that's
// the honest state for a catalog that has never synced, e.g. ps_plus
// while its syncer is still a placeholder.
func snapshotAge(dataDir, slug string) string {
	data, err := os.ReadFile(filepath.Join(dataDir, "catalogs", slug+".json"))
	if err != nil {
		return "never"
	}
	var snap catalogSnapshotFetchedAt
	if err := json.Unmarshal(data, &snap); err != nil || snap.FetchedAt == "" {
		return "never"
	}
	return snap.FetchedAt
}
