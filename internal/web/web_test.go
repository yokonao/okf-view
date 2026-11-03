package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"b/index.md":   "---\nokf_version: \"0.2\"\n---\n# Bundle B\n\n* [A](a.md)\n* [Sub](sub/)\n",
		"b/a.md":       "---\ntype: Rule\n---\n[C](/sub/c.md) [gone](gone.md) [img](pic.png) <script>x</script>\n",
		"b/sub/c.md":   "---\ntype: Rule\n---\n[A](/a.md) [idx](/index.md)\n",
		"b/sub/log.md": "# Log\n",
		"b/pic.png":    "png",
		"b/s.md": `---
type: Rule
sources:
  - { id: thread, resource: "https://example.com/t", title: "A <thread>" }
  - { id: a_b, resource: a.md }
  - { id: gone, resource: gone.md }
  - { id: js, resource: "javascript:alert(1)" }
  - { id: note }
---
Kept by hand（source: thread）, see（source: a_b, nope）.
` + "`（source: thread）`" + `
`,
		"secret.txt":    "secret",
		"b/notes/x.txt": "x",
	}
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer((&Server{Dir: dir, Now: func() time.Time { return time.Unix(0, 0) }}).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return res.StatusCode, sb.String()
}

func TestData(t *testing.T) {
	srv := newServer(t)
	code, body := get(t, srv.URL+"/api/data")
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	var d dataJSON
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatal(err)
	}
	pages := map[string]pageJSON{}
	for _, p := range d.Pages {
		pages[p.Key] = p
	}
	root := pages["b/"]
	if root.Kind != "dir" || root.Title != "b" || !strings.Contains(root.HTML, "<h1>Bundle B</h1>") {
		t.Errorf("root = %+v", root)
	}
	for _, want := range []string{`href="#b/a.md"`, `href="#b/sub/"`} {
		if !strings.Contains(root.HTML, want) {
			t.Errorf("root html lacks %s: %s", want, root.HTML)
		}
	}
	a := pages["b/a.md"]
	for _, want := range []string{`href="#b/sub/c.md"`, `class="broken"`, `href="files/b/pic.png"`, "raw HTML omitted"} {
		if !strings.Contains(a.HTML, want) {
			t.Errorf("a html lacks %s: %s", want, a.HTML)
		}
	}
	if !slices.Equal(a.Backlinks, []string{"b/s.md", "b/sub/c.md"}) {
		t.Errorf("a backlinks = %v", a.Backlinks)
	}
	if c := pages["b/sub/c.md"]; !strings.Contains(c.HTML, `href="#b/"`) {
		t.Errorf("index link not to the directory: %s", c.HTML)
	}
	if sub := pages["b/sub/"]; sub.Log != "b/sub/log.md" || pages["b/sub/log.md"].Kind != "log" {
		t.Errorf("sub log = %q", sub.Log)
	}
	if _, ok := pages["b/notes/"]; !ok {
		t.Error("directory without index.md has no page")
	}
}

func TestSources(t *testing.T) {
	srv := newServer(t)
	_, body := get(t, srv.URL+"/api/data")
	var d dataJSON
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatal(err)
	}
	var s pageJSON
	for _, p := range d.Pages {
		if p.Key == "b/s.md" {
			s = p
		}
	}
	want := []sourceJSON{
		{ID: "thread", Title: "A <thread>", Resource: "https://example.com/t", Href: "https://example.com/t"},
		{ID: "a_b", Title: "a_b", Resource: "a.md", Href: "#b/a.md"},
		{ID: "gone", Title: "gone", Resource: "gone.md", Broken: true},
		{ID: "js", Title: "js", Resource: "javascript:alert(1)"},
		{ID: "note", Title: "note"},
	}
	if !slices.Equal(s.Sources, want) {
		t.Errorf("sources = %+v", s.Sources)
	}
	for _, w := range []string{
		`Kept by hand（source: <a href="https://example.com/t" title="A &lt;thread&gt;">thread</a>）, see`,
		`（source: <a href="#b/a.md" title="a_b">a_b</a>, <a class="broken" title="Not in sources: nope">nope</a>）.`,
		"<code>（source: thread）</code>",
	} {
		if !strings.Contains(s.HTML, w) {
			t.Errorf("html lacks %s: %s", w, s.HTML)
		}
	}
}

func TestFile(t *testing.T) {
	srv := newServer(t)
	if code, body := get(t, srv.URL+"/files/b/pic.png"); code != 200 || body != "png" {
		t.Errorf("pic.png: %d %q", code, body)
	}
	for _, p := range []string{"/files/b/..%2Fsecret.txt", "/files/b/missing.png", "/files/x/pic.png"} {
		if code, _ := get(t, srv.URL+p); code != 404 {
			t.Errorf("%s: status %d, want 404", p, code)
		}
	}
}
