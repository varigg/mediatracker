package server

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
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

func newViews() *views {
	pages := map[string]*template.Template{}
	for _, page := range []string{"home.html", "tab.html", "detail.html"} {
		t := template.Must(template.ParseFS(templatesFS,
			"templates/layout.html", "templates/"+page))
		pages[page] = t
	}
	return &views{pages: pages}
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
