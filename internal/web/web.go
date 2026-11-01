// Package web serves a browser UI over OKF bundles.
package web

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"

	"github.com/yokonao/okf-view/internal/okf"
)

//go:embed index.html
var indexHTML []byte

type Server struct {
	Dir string
	Now func() time.Time
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Bundle content is rendered without raw HTML, but keep scripts to the page's own.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' data:")
		_, _ = w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/data", s.data)
	mux.HandleFunc("GET /files/{bundle}/{path...}", s.file)
	return mux
}

func (s *Server) load() ([]*okf.Bundle, error) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	return okf.Load(s.Dir, now())
}

type skipJSON struct {
	Rel    string `json:"rel"`
	Reason string `json:"reason"`
}

type bundleJSON struct {
	Name       string     `json:"name"`
	Root       string     `json:"root"`
	OKFVersion string     `json:"okfVersion,omitempty"`
	Skipped    []skipJSON `json:"skipped"`
}

// pageJSON is a concept, a directory (with its index.md, if any) or a log.md.
// Key is "<bundle>/<path>", with a trailing "/" for a directory.
type pageJSON struct {
	Key         string      `json:"key"`
	Kind        string      `json:"kind"`
	Bundle      string      `json:"bundle"`
	Rel         string      `json:"rel"`
	Dir         string      `json:"dir"`
	File        string      `json:"file,omitempty"`
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	Type        string      `json:"type,omitempty"`
	Tags        []string    `json:"tags,omitempty"`
	Status      string      `json:"status,omitempty"`
	StaleAfter  string      `json:"staleAfter,omitempty"`
	Stale       bool        `json:"stale,omitempty"`
	Trust       okf.Trust   `json:"trust,omitempty"`
	Verified    []okf.Stamp `json:"verified,omitempty"`
	Generated   *okf.Stamp  `json:"generated,omitempty"`
	Modified    *time.Time  `json:"modified,omitempty"`
	Problem     string      `json:"problem,omitempty"`
	Body        string      `json:"body,omitempty"`
	HTML        string      `json:"html"`
	Backlinks   []string    `json:"backlinks,omitempty"`
	Log         string      `json:"log,omitempty"`
}

type dataJSON struct {
	Dir     string       `json:"dir"`
	Bundles []bundleJSON `json:"bundles"`
	Pages   []pageJSON   `json:"pages"`
}

func (s *Server) data(w http.ResponseWriter, _ *http.Request) {
	bundles, err := s.load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := dataJSON{Dir: s.Dir, Bundles: []bundleJSON{}, Pages: []pageJSON{}}
	for _, b := range bundles {
		bj := bundleJSON{Name: b.Name, Root: b.Root, OKFVersion: b.OKFVersion, Skipped: []skipJSON{}}
		for _, sk := range b.Skipped {
			bj.Skipped = append(bj.Skipped, skipJSON(sk))
		}
		out.Bundles = append(out.Bundles, bj)
		out.Pages = append(out.Pages, pages(b)...)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func pages(b *okf.Bundle) []pageJSON {
	var out []pageJSON
	dirs := make([]string, 0, len(b.Dirs))
	for d := range b.Dirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		p := pageJSON{Key: dirKey(b.Name, d), Kind: "dir", Bundle: b.Name, Rel: d, Dir: path.Dir(d), Title: path.Base(d)}
		if d == "" {
			p.Dir, p.Title = "", b.Name
		} else if p.Dir == "." {
			p.Dir = ""
		}
		if idx := b.Indexes[d]; idx != nil {
			_, body, _ := okf.Frontmatter(idx.Body)
			if title, rest, ok := leadingH1(body); ok {
				p.Title, body = title, rest
			}
			p.File, p.Body, p.Modified = idx.Path, body, &idx.Modified
			p.HTML = render(b, path.Join(d, "index"), body)
		}
		if lg := b.Logs[d]; lg != nil {
			rel := path.Join(d, "log.md")
			p.Log = b.Name + "/" + rel
			out = append(out, pageJSON{
				Key: p.Log, Kind: "log", Bundle: b.Name, Rel: rel, Dir: d, File: lg.Path,
				Title: "log.md", Body: lg.Body, Modified: &lg.Modified,
				HTML: render(b, path.Join(d, "log"), lg.Body),
			})
		}
		out = append(out, p)
	}
	for _, c := range b.Concepts {
		dir := path.Dir(c.ID)
		if dir == "." {
			dir = ""
		}
		p := pageJSON{
			Key: b.Name + "/" + c.ID + ".md", Kind: "concept", Bundle: b.Name, Rel: c.ID, Dir: dir, File: c.Path,
			Title: c.Title, Description: c.Description, Type: c.Type, Tags: c.Tags,
			Status: c.Status, StaleAfter: c.StaleAfter, Stale: c.Stale, Trust: c.Trust,
			Verified: c.Verified, Generated: c.Generated, Modified: &c.Modified, Problem: c.Problem,
			Body: c.Body, HTML: render(b, c.ID, c.Body),
		}
		for _, from := range b.Backlinks[c.ID] {
			p.Backlinks = append(p.Backlinks, b.Name+"/"+from+".md")
		}
		out = append(out, p)
	}
	return out
}

func dirKey(bundle, dir string) string {
	if dir == "" {
		return bundle + "/"
	}
	return bundle + "/" + dir + "/"
}

var h1Re = regexp.MustCompile(`^\s*# +(.+?)\s*#*\s*(?:\n|$)`)

// leadingH1 splits off a heading the page shows as its title.
func leadingH1(body string) (string, string, bool) {
	m := h1Re.FindStringSubmatchIndex(body)
	if m == nil {
		return "", body, false
	}
	return body[m[2]:m[3]], body[m[1]:], true
}

var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

// render converts Markdown to HTML with links within the bundle pointing at
// the UI's pages. fromID is the document's path without ".md". Raw HTML in
// the source is escaped.
func render(b *okf.Bundle, fromID, body string) string {
	src := []byte(body)
	doc := md.Parser().Parse(text.NewReader(src))
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Link:
			rewrite(b, fromID, n, &n.Destination)
		case *ast.Image:
			rewrite(b, fromID, n, &n.Destination)
		}
		return ast.WalkContinue, nil
	})
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return "<pre>" + err.Error() + "</pre>"
	}
	return buf.String()
}

func rewrite(b *okf.Bundle, fromID string, n ast.Node, dest *[]byte) {
	raw := string(*dest)
	if okf.IsExternal(raw) {
		return
	}
	rel, ok := b.Resolve(fromID, raw)
	if !ok {
		n.SetAttributeString("class", []byte("broken"))
		n.SetAttributeString("title", []byte("Not in the bundle: "+raw))
		return
	}
	*dest = []byte(href(b, rel))
}

// href is where the UI shows the file or directory at rel.
func href(b *okf.Bundle, rel string) string {
	switch {
	case b.Dirs[rel]:
		return "#" + dirKey(b.Name, rel)
	case path.Base(rel) == "index.md":
		dir := path.Dir(rel)
		if dir == "." {
			dir = ""
		}
		return "#" + dirKey(b.Name, dir)
	case strings.HasSuffix(rel, ".md"):
		return "#" + b.Name + "/" + rel
	}
	return "files/" + b.Name + "/" + rel
}

// file serves a file in a bundle, such as an image a concept embeds.
func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	bundles, err := s.load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	name, rel := r.PathValue("bundle"), r.PathValue("path")
	for _, b := range bundles {
		// Only files the scan took in, so links leading out of the bundle are not followed.
		if b.Name != name || !b.Files[rel] {
			continue
		}
		f, err := os.Open(b.Root + "/" + rel)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Security-Policy", "sandbox")
		http.ServeContent(w, r, path.Base(rel), info.ModTime(), f)
		return
	}
	http.NotFound(w, r)
}
