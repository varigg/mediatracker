package server

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/url"

	"github.com/varigg/mediatracker/internal/store"
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

// tmplFuncs are shared across every page template. args builds the data
// map "th" needs to render one sortable column header; tabURL and kv let
// every toolbar control (state tabs, subtabs, the available checkbox)
// build its target URL from the current filter state plus the one
// dimension it changes.
var tmplFuncs = template.FuncMap{
	"args": func(d TabData, sort, label string) map[string]any {
		href, glyph := sortLink(d, sort)
		return map[string]any{"Href": href, "Glyph": glyph, "Label": label}
	},
	"tabURL": tabURL,
	// kv builds the overrides map tabURL takes, from a flat key/value
	// argument list — template ergonomics, since html/template call
	// syntax has no map literal. Values are stringified with fmt.Sprint
	// so a typed value (e.g. store.State) can be passed directly.
	"kv": func(pairs ...any) map[string]string {
		m := make(map[string]string, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			m[fmt.Sprint(pairs[i])] = fmt.Sprint(pairs[i+1])
		}
		return m
	},
}

func newViews() *views {
	pages := map[string]*template.Template{}
	for _, page := range []string{"home.html", "tab.html", "detail.html", "settings.html"} {
		t := template.Must(template.New("layout.html").Funcs(tmplFuncs).ParseFS(templatesFS,
			"templates/layout.html", "templates/partials.html", "templates/"+page))
		pages[page] = t
	}
	// search.html is a fragment-only set: no layout, just the
	// search-results define plus partials.html for the "thumb" partial
	// its candidate rows reuse.
	pages["search.html"] = template.Must(template.New("search.html").Funcs(tmplFuncs).ParseFS(templatesFS,
		"templates/partials.html", "templates/search.html"))
	return &views{pages: pages}
}

// tabURL builds one toolbar control's target URL. It starts from d's
// current filter state — state, type (when d.Sub is set), genre,
// available, sort, dir — then applies overrides on top; an override
// mapping a key to "" removes that param. Empty/unset dimensions are
// omitted so URLs stay clean. Every toolbar control (state tabs, subtabs,
// the available checkbox, sort headers via sortLink) renders through
// this one function, so each carries every filter dimension except the
// one it changes.
func tabURL(d TabData, overrides map[string]string) string {
	f := d.Filter

	cur := map[string]string{
		"state": string(f.State),
		"type":  d.Sub,
		"genre": f.Genre,
		"sort":  f.Sort,
		"dir":   f.Dir,
	}
	if f.Available {
		cur["available"] = "1"
	}
	for k, val := range overrides {
		cur[k] = val
	}

	v := url.Values{}
	for _, k := range []string{"state", "type", "genre", "available", "sort", "dir"} {
		if val := cur[k]; val != "" {
			v.Set(k, val)
		}
	}
	if len(v) == 0 {
		return "/" + d.Group
	}
	return "/" + d.Group + "?" + v.Encode()
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
	defDir := store.DefaultDir(sort)

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

	href = tabURL(d, map[string]string{"sort": sort, "dir": dir})
	return href, glyph
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
	return v.executeBlock(w, page, block, data)
}

// executeBlock writes one named {{define}} block to an arbitrary
// io.Writer, without touching response headers — used when the caller
// needs to set a non-200 status code before the body is written.
func (v *views) executeBlock(w io.Writer, page, block string, data any) error {
	t, ok := v.pages[page]
	if !ok {
		return fmt.Errorf("server: unknown page %q", page)
	}
	return t.ExecuteTemplate(w, block, data)
}
