// Package okf reads Open Knowledge Format bundles the way cc-okf does.
package okf

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Dir returns the bundles directory: dir when set, otherwise $OKF_DIR.
// A leading ~ means the home directory.
func Dir(dir string) (string, error) {
	if dir == "" {
		dir = os.Getenv("OKF_DIR")
	}
	if dir == "" {
		return "", errors.New("no bundles directory: pass one or set OKF_DIR")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return expandHome(dir, home), nil
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return p
}

type Trust string

const (
	Unverified       Trust = "unverified"
	MachineConfirmed Trust = "machine-confirmed"
	HumanReviewed    Trust = "human-reviewed"
)

type Stamp struct {
	By string `json:"by"`
	At string `json:"at,omitempty"`
}

type Concept struct {
	// ID is the path within the bundle without ".md" (OKF §2).
	ID          string
	Path        string
	Modified    time.Time
	Type        string
	Title       string
	Description string
	Tags        []string
	// Status is "stable" when absent (OKF §5.4).
	Status     string
	StaleAfter string
	Stale      bool
	Verified   []Stamp
	Trust      Trust
	Generated  *Stamp
	Body       string
	// Refs are link targets as written in the body and sources.
	Refs    []string
	Problem string
}

// Doc is an index.md or log.md.
type Doc struct {
	Path     string
	Modified time.Time
	Body     string
}

type Bundle struct {
	Name       string
	Root       string
	OKFVersion string
	Concepts   map[string]*Concept
	// Files and Dirs are paths within the bundle; "" is the root directory.
	Files   map[string]bool
	Dirs    map[string]bool
	Indexes map[string]*Doc
	Logs    map[string]*Doc
	// Backlinks maps a concept ID to the IDs of the concepts linking to it.
	Backlinks map[string][]string
	Skipped   []Skip
}

type Skip struct {
	Rel    string
	Reason string
}

// Load scans every bundle: the directories, or links to directories,
// directly under dir. A dir with its own index.md is a bundle itself.
// A missing dir holds no bundles.
func Load(dir string, now time.Time) ([]*Bundle, error) {
	if _, err := os.Stat(filepath.Join(dir, "index.md")); err == nil {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		b, err := scan(filepath.Base(abs), dir, now)
		if err != nil {
			return nil, err
		}
		return []*Bundle{b}, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var bundles []*Bundle
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		root := filepath.Join(dir, e.Name())
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			continue
		}
		b, err := scan(e.Name(), root, now)
		if err != nil {
			return nil, err
		}
		bundles = append(bundles, b)
	}
	return bundles, nil
}

func scan(name, root string, now time.Time) (*Bundle, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	b := &Bundle{
		Name:      name,
		Root:      root,
		Concepts:  map[string]*Concept{},
		Files:     map[string]bool{},
		Dirs:      map[string]bool{"": true},
		Indexes:   map[string]*Doc{},
		Logs:      map[string]*Doc{},
		Backlinks: map[string][]string{},
	}
	// Real paths already walked, so a link back into the tree is not walked again.
	walked := map[string]bool{realRoot: true}
	var walk func(dirRel string) error
	walk = func(dirRel string) error {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dirRel)))
		if err != nil {
			return err
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" {
				continue
			}
			rel := path.Join(dirRel, e.Name())
			if err := b.take(e, dirRel, rel, realRoot, walked, walk, now); err != nil {
				b.Skipped = append(b.Skipped, Skip{Rel: rel, Reason: err.Error()})
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return nil, err
	}
	if idx := b.Indexes[""]; idx != nil {
		data, _, _ := Frontmatter(idx.Body)
		if v, ok := data["okf_version"]; ok && v != nil {
			b.OKFVersion = fmt.Sprint(v)
		}
	}
	b.linkBack()
	return b, nil
}

func (b *Bundle) take(e fs.DirEntry, dirRel, rel, realRoot string, walked map[string]bool, walk func(string) error, now time.Time) error {
	p := filepath.Join(b.Root, filepath.FromSlash(rel))
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	if info.IsDir() {
		target, err := filepath.EvalSymlinks(p)
		if err != nil {
			return err
		}
		if r, err := filepath.Rel(realRoot, target); err != nil || r == ".." || strings.HasPrefix(r, "../") {
			return fmt.Errorf("links to %s, outside the bundle", target)
		}
		if walked[target] {
			return fmt.Errorf("%s is already walked through another path", target)
		}
		walked[target] = true
		b.Dirs[rel] = true
		return walk(rel)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	b.Files[rel] = true
	if !strings.HasSuffix(rel, ".md") {
		return nil
	}
	src, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	doc := &Doc{Path: p, Modified: info.ModTime(), Body: string(src)}
	switch e.Name() {
	case "index.md":
		b.Indexes[dirRel] = doc
	case "log.md":
		b.Logs[dirRel] = doc
	default:
		c := parseConcept(strings.TrimSuffix(rel, ".md"), doc, now)
		b.Concepts[c.ID] = c
	}
	return nil
}

func (b *Bundle) linkBack() {
	for _, c := range b.Concepts {
		for _, ref := range c.Refs {
			rel, ok := b.Resolve(c.ID, ref)
			if !ok || !strings.HasSuffix(rel, ".md") {
				continue
			}
			target := strings.TrimSuffix(rel, ".md")
			if target == c.ID || b.Concepts[target] == nil {
				continue
			}
			b.Backlinks[target] = append(b.Backlinks[target], c.ID)
		}
	}
	for id, from := range b.Backlinks {
		sort.Strings(from)
		b.Backlinks[id] = slices.Compact(from)
	}
}

var externalRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// IsExternal reports whether target points outside the bundle.
func IsExternal(target string) bool {
	return externalRe.MatchString(target) || strings.HasPrefix(target, "//") || strings.HasPrefix(target, "#")
}

// Resolve returns the path within the bundle that target, as written in the
// document at fromID (a path without ".md"), points to, and whether it exists.
// A target starting with "/" is relative to the bundle root (OKF §6).
func (b *Bundle) Resolve(fromID, target string) (string, bool) {
	if IsExternal(target) {
		return "", false
	}
	clean, _, _ := strings.Cut(target, "#")
	clean, _, _ = strings.Cut(clean, "?")
	clean, err := url.PathUnescape(clean)
	if err != nil || clean == "" {
		return "", false
	}
	exists := func(rel string) bool { return b.Files[rel] || b.Dirs[rel] }
	if strings.HasPrefix(clean, "/") {
		rel := strings.TrimPrefix(path.Clean(clean), "/")
		return rel, exists(rel)
	}
	fromDir := path.Join(path.Dir(fromID), clean)
	if exists(fromDir) {
		return fromDir, true
	}
	// OKF §6 says relative to the concept, yet producers often write paths from the bundle root.
	if fromRoot := path.Clean(clean); !strings.HasPrefix(fromRoot, "..") && exists(fromRoot) {
		return fromRoot, true
	}
	if strings.HasPrefix(fromDir, "..") {
		return "", false
	}
	return fromDir, false
}

func parseConcept(id string, doc *Doc, now time.Time) *Concept {
	data, body, problem := Frontmatter(doc.Body)
	str := func(v any) string {
		s, _ := v.(string)
		if strings.TrimSpace(s) == "" {
			return ""
		}
		return s
	}
	c := &Concept{
		ID:          id,
		Path:        doc.Path,
		Modified:    doc.Modified,
		Type:        str(data["type"]),
		Title:       str(data["title"]),
		Description: str(data["description"]),
		Tags:        stringList(data["tags"]),
		Status:      str(data["status"]),
		StaleAfter:  str(data["stale_after"]),
		Verified:    stamps(data["verified"]),
		Body:        body,
		Problem:     problem,
	}
	if c.Title == "" {
		c.Title = path.Base(id)
	}
	if c.Status == "" {
		c.Status = "stable"
	}
	if g := stamps(data["generated"]); len(g) > 0 {
		c.Generated = &g[0]
	}
	c.Trust = Unverified
	if len(c.Verified) > 0 {
		c.Trust = MachineConfirmed
		for _, v := range c.Verified {
			if strings.HasPrefix(v.By, "human:") {
				c.Trust = HumanReviewed
			}
		}
	}
	c.Stale = isStale(c.StaleAfter, now)
	if c.Problem == "" && c.Type == "" {
		c.Problem = "frontmatter lacks the required `type`"
	}
	c.Refs = bodyLinks(body)
	if sources, ok := data["sources"].([]any); ok {
		for _, s := range sources {
			if m, ok := s.(map[string]any); ok && str(m["resource"]) != "" {
				c.Refs = append(c.Refs, str(m["resource"]))
			}
		}
	}
	return c
}

var staleLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02"}

// isStale reports whether now is at or past staleAfter (OKF §5.5).
func isStale(staleAfter string, now time.Time) bool {
	for _, layout := range staleLayouts {
		if t, err := time.Parse(layout, staleAfter); err == nil {
			return !now.Before(t)
		}
	}
	return false
}

// stamps reads `verified` or `generated`: a mapping or a list of them (OKF §5.2).
func stamps(v any) []Stamp {
	list, ok := v.([]any)
	if !ok {
		list = []any{v}
	}
	var out []Stamp
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		by, _ := m["by"].(string)
		if by == "" {
			continue
		}
		at, _ := m["at"].(string)
		out = append(out, Stamp{By: by, At: at})
	}
	return out
}

func stringList(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

var blockRe = regexp.MustCompile(`^---[ \t]*\r?\n(?:([\s\S]*?)\r?\n)?---[ \t]*(?:\r?\n|$)`)

// Frontmatter splits a document into its YAML frontmatter, its body and a
// problem with the frontmatter. Timestamps stay strings, as OKF compares
// them textually.
func Frontmatter(text string) (map[string]any, string, string) {
	src := strings.TrimPrefix(text, "\uFEFF")
	m := blockRe.FindStringSubmatchIndex(src)
	if m == nil {
		return map[string]any{}, src, "no frontmatter block"
	}
	body := src[m[1]:]
	var node yaml.Node
	if m[2] >= 0 {
		if err := yaml.NewDecoder(strings.NewReader(src[m[2]:m[3]])).Decode(&node); err != nil && !errors.Is(err, io.EOF) {
			msg, _, _ := strings.Cut(err.Error(), "\n")
			return map[string]any{}, body, "frontmatter is not valid YAML: " + msg
		}
	}
	if node.Kind == 0 {
		return map[string]any{}, body, ""
	}
	keepTimestamps(&node)
	var data any
	if err := node.Decode(&data); err != nil {
		return map[string]any{}, body, "frontmatter is not valid YAML: " + err.Error()
	}
	switch d := data.(type) {
	case nil:
		return map[string]any{}, body, ""
	case map[string]any:
		return d, body, ""
	}
	return map[string]any{}, body, "frontmatter is not a mapping"
}

// keepTimestamps makes timestamps decode as the strings they are written as.
func keepTimestamps(n *yaml.Node) {
	if n.Kind == yaml.ScalarNode && n.ShortTag() == "!!timestamp" {
		n.Tag = "!!str"
	}
	for _, c := range n.Content {
		keepTimestamps(c)
	}
}

var (
	fenceRe = regexp.MustCompile("(?ms)^(```|~~~).*?^(```|~~~)")
	linkRe  = regexp.MustCompile(`(!?)\[[^\]]*\]\(\s*<?([^)\s>]+)>?(?:\s+["'][^)]*["'])?\s*\)`)
)

func bodyLinks(body string) []string {
	prose := fenceRe.ReplaceAllString(body, "")
	var out []string
	seen := map[string]bool{}
	for _, m := range linkRe.FindAllStringSubmatch(prose, -1) {
		if m[1] == "!" || seen[m[2]] {
			continue
		}
		seen[m[2]] = true
		out = append(out, m[2])
	}
	return out
}
