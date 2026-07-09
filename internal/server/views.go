package server

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed assets/*
var rawAssetsFS embed.FS

func assetsFS() fs.FS {
	sub, err := fs.Sub(rawAssetsFS, "assets")
	if err != nil {
		panic(err) // embedded path is a compile-time fact; boot-time failure is correct
	}
	return sub
}

// views holds the parsed template set. Every page template is parsed
// together with layout.html so pages fill layout's {{block}} slots.
type views struct {
	pages map[string]*template.Template
}

// tmplFuncs are shared across every page template. args builds the
// data map "th" needs to render one sortable column header.
var tmplFuncs = template.FuncMap{
	"args": func(d TabData, sort, label string) map[string]any {
		href, glyph := sortLink(d, sort)
		return map[string]any{"Href": href, "Glyph": glyph, "Label": label}
	},
}

func newViews() *views {
	pages := map[string]*template.Template{}
	for _, page := range []string{"home.html", "tab.html", "detail.html"} {
		t := template.Must(template.New("layout.html").Funcs(tmplFuncs).ParseFS(templatesFS,
			"templates/layout.html", "templates/"+page))
		pages[page] = t
	}
	return &views{pages: pages}
}

// sortLink computes a column header's target URL (current filter, this
// column as sort, direction toggled if already active) and the glyph
// showing the active direction — a pure function so it's unit-testable
// without going through the template engine.
func sortLink(d TabData, sort string) (href string, glyph string) {
	f := d.Filter

	curSort := f.Sort
	if curSort == "" {
		curSort = "added"
	}
	defDir := "desc"
	if sort == "title" {
		defDir = "asc"
	}

	dir := defDir
	if curSort == sort {
		curDir := f.Dir
		if curDir == "" {
			curDir = defDir
		}
		if curDir == "asc" {
			dir = "desc"
			glyph = "↑"
		} else {
			dir = "asc"
			glyph = "↓"
		}
	}

	v := url.Values{}
	if f.State != "" {
		v.Set("state", string(f.State))
	}
	if d.Sub != "" {
		v.Set("type", d.Sub)
	}
	if f.Available {
		v.Set("available", "1")
	}
	v.Set("sort", sort)
	v.Set("dir", dir)
	return "/" + d.Group + "?" + v.Encode(), glyph
}

// render writes the full page (layout + page).
func (v *views) render(w http.ResponseWriter, page string, data any) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t, ok := v.pages[page]
	if !ok {
		return fmt.Errorf("server: unknown page %q", page)
	}
	return t.ExecuteTemplate(w, "layout", data)
}

// renderBlock writes one named {{define}} block from a page — the HTMX
// fragment path.
func (v *views) renderBlock(w http.ResponseWriter, page, block string, data any) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t, ok := v.pages[page]
	if !ok {
		return fmt.Errorf("server: unknown page %q", page)
	}
	return t.ExecuteTemplate(w, block, data)
}
