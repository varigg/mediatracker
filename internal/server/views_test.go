package server

import (
	"net/url"
	"testing"

	"github.com/varigg/mediatracker/internal/store"
)

func TestSortLink(t *testing.T) {
	// Test cases covering:
	// - clicking NEW column (current sort=added, click year): href contains sort=year with year's default direction (desc), no glyph
	// - clicking ACTIVE column toggles direction (sort=year dir desc → href has dir=asc), shows glyph
	// - title's default direction is asc (title is the one asc-default sort)
	// - hrefs preserve state/type/available from current TabData

	tests := []struct {
		name        string
		d           TabData
		sort        string
		wantHref    string
		wantGlyph   string
		description string
	}{
		{
			name: "click new column year from default added sort",
			d: TabData{
				Group: "movies",
				Filter: store.ListFilter{
					Sort: "", // defaults to "added"
					Dir:  "",
				},
			},
			sort:        "year",
			wantHref:    "/movies?dir=desc&sort=year",
			wantGlyph:   "",
			description: "new column gets default direction (desc for year), no glyph",
		},
		{
			name: "click new column title from default added sort",
			d: TabData{
				Group: "shows",
				Filter: store.ListFilter{
					Sort: "",
					Dir:  "",
				},
			},
			sort:        "title",
			wantHref:    "/shows?dir=asc&sort=title",
			wantGlyph:   "",
			description: "title defaults to asc when clicked new",
		},
		{
			name: "toggle active column from desc to asc",
			d: TabData{
				Group: "movies",
				Filter: store.ListFilter{
					Sort: "year",
					Dir:  "desc",
				},
			},
			sort:        "year",
			wantHref:    "/movies?dir=asc&sort=year",
			wantGlyph:   "↓",
			description: "active desc toggles to asc, shows desc glyph (↓)",
		},
		{
			name: "toggle active column from asc to desc",
			d: TabData{
				Group: "shows",
				Filter: store.ListFilter{
					Sort: "title",
					Dir:  "asc",
				},
			},
			sort:        "title",
			wantHref:    "/shows?dir=desc&sort=title",
			wantGlyph:   "↑",
			description: "active asc toggles to desc, shows asc glyph (↑)",
		},
		{
			name: "active column with empty dir uses default",
			d: TabData{
				Group: "movies",
				Filter: store.ListFilter{
					Sort: "added",
					Dir:  "",
				},
			},
			sort:        "added",
			wantHref:    "/movies?dir=asc&sort=added",
			wantGlyph:   "↓",
			description: "active column with empty dir toggles from default (desc) to asc",
		},
		{
			name: "new column preserves state filter",
			d: TabData{
				Group: "movies",
				Filter: store.ListFilter{
					Sort:  "added",
					Dir:   "desc",
					State: "watching",
				},
			},
			sort:        "title",
			wantHref:    "/movies?dir=asc&sort=title&state=watching",
			wantGlyph:   "",
			description: "href preserves state parameter",
		},
		{
			name: "new column preserves type (sub)",
			d: TabData{
				Group:  "all",
				Sub:    "movie",
				Filter: store.ListFilter{Sort: "added", Dir: "desc"},
			},
			sort:        "year",
			wantHref:    "/all?dir=desc&sort=year&type=movie",
			wantGlyph:   "",
			description: "href preserves type parameter from Sub",
		},
		{
			name: "new column preserves available flag",
			d: TabData{
				Group: "movies",
				Filter: store.ListFilter{
					Sort:      "added",
					Dir:       "desc",
					Available: true,
				},
			},
			sort:        "year",
			wantHref:    "/movies?available=1&dir=desc&sort=year",
			wantGlyph:   "",
			description: "href preserves available parameter",
		},
		{
			name: "new column preserves state, type, and available",
			d: TabData{
				Group: "all",
				Sub:   "tv",
				Filter: store.ListFilter{
					Sort:      "added",
					Dir:       "desc",
					State:     "completed",
					Available: true,
				},
			},
			sort:        "title",
			wantHref:    "/all?available=1&dir=asc&sort=title&state=completed&type=tv",
			wantGlyph:   "",
			description: "href preserves all filter parameters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			href, glyph := sortLink(tt.d, tt.sort)

			// Parse both URLs to compare them in a canonical order
			// (url.Values.Encode() sorts parameters)
			wantURLParsed, err := url.Parse(tt.wantHref)
			if err != nil {
				t.Fatalf("invalid wantHref: %v", err)
			}
			wantQuery := wantURLParsed.RawQuery

			gotURLParsed, err := url.Parse(href)
			if err != nil {
				t.Fatalf("invalid href from sortLink: %v", err)
			}
			gotQuery := gotURLParsed.RawQuery

			if gotQuery != wantQuery {
				t.Errorf("href query mismatch\ngot:  %s\nwant: %s", gotQuery, wantQuery)
			}

			if glyph != tt.wantGlyph {
				t.Errorf("glyph mismatch: got %q, want %q", glyph, tt.wantGlyph)
			}
		})
	}
}
