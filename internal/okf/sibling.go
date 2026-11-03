package okf

import (
	"path"
	"slices"
	"sort"
	"strings"
)

// Links into sibling bundles are an okf-view extension, not part of OKF v0.2,
// which only resolves links within a bundle (§6.1). A relative link leaving
// the bundle by one level, `../<name>/<rest>`, resolves into the bundle
// <name> loaded from the same directory. The spec allows it: such a link is
// a relative path (§6.2) and, to a consumer without the extension, a broken
// link it must tolerate (§11). Drop this file, SiblingBacklinks and their
// callers once OKF defines links across bundles.

// Ref names a concept in a bundle.
type Ref struct {
	Bundle string
	ID     string
}

// ResolveSibling returns the sibling bundle and the path within it that
// target, as written in the document fromID, points to, and whether it does.
func (b *Bundle) ResolveSibling(fromID, target string) (*Bundle, string, bool) {
	clean := cleanTarget(target)
	if clean == "" || strings.HasPrefix(clean, "/") {
		return nil, "", false
	}
	// From the concept, then from the bundle root, as Resolve does.
	for _, p := range []string{path.Join(path.Dir(fromID), clean), path.Clean(clean)} {
		rest, ok := strings.CutPrefix(p, "../")
		if !ok {
			continue
		}
		name, rel, _ := strings.Cut(rest, "/")
		for _, o := range b.siblings {
			if o != b && o.Name == name && (o.Files[rel] || o.Dirs[rel]) {
				return o, rel, true
			}
		}
	}
	return nil, "", false
}

func linkSiblings(bs []*Bundle) {
	for _, b := range bs {
		b.siblings = bs
	}
	for _, b := range bs {
		for _, c := range b.Concepts {
			for _, ref := range c.Refs {
				o, rel, ok := b.ResolveSibling(c.ID, ref)
				id, isMD := strings.CutSuffix(rel, ".md")
				if !ok || !isMD || o.Concepts[id] == nil {
					continue
				}
				if o.SiblingBacklinks == nil {
					o.SiblingBacklinks = map[string][]Ref{}
				}
				o.SiblingBacklinks[id] = append(o.SiblingBacklinks[id], Ref{Bundle: b.Name, ID: c.ID})
			}
		}
	}
	for _, b := range bs {
		for id, from := range b.SiblingBacklinks {
			sort.Slice(from, func(i, j int) bool {
				return from[i].Bundle+"/"+from[i].ID < from[j].Bundle+"/"+from[j].ID
			})
			b.SiblingBacklinks[id] = slices.Compact(from)
		}
	}
}
