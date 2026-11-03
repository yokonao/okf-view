package web

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/yokonao/okf-view/internal/okf"
)

// citeRe matches a body's reference to sources by id, a convention of these
// bundles rather than of OKF: "（source: a, b）".
var citeRe = regexp.MustCompile(`（source:[ \t]*([^\s,（）]+(?:[ \t]*,[ \t]*[^\s,（）]+)*)[ \t]*）`)

var kindCite = ast.NewNodeKind("Cite")

type citeNode struct {
	ast.BaseInline
	Sources []sourceJSON
}

func (n *citeNode) Kind() ast.NodeKind { return kindCite }

func (n *citeNode) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

type sourceJSON struct {
	ID       string `json:"id,omitempty"`
	Title    string `json:"title"`
	Resource string `json:"resource,omitempty"`
	// Href is empty for a source without a resource or one missing from the bundle.
	Href   string `json:"href,omitempty"`
	Broken bool   `json:"broken,omitempty"`
}

func sources(b *okf.Bundle, c *okf.Concept) []sourceJSON {
	var out []sourceJSON
	for _, s := range c.Sources {
		sj := sourceJSON{ID: s.ID, Title: s.Title, Resource: s.Resource}
		if sj.Title == "" {
			sj.Title = s.ID
		}
		if sj.Title == "" {
			sj.Title = s.Resource
		}
		switch {
		case s.Resource == "":
		case okf.IsExternal(s.Resource):
			if !html.IsDangerousURL([]byte(s.Resource)) {
				sj.Href = s.Resource
			}
		default:
			if h, ok := link(b, c.ID, s.Resource); ok {
				sj.Href = h
			} else {
				sj.Broken = true
			}
		}
		out = append(out, sj)
	}
	return out
}

// cite replaces citations in the text under doc with citeNodes.
func cite(doc ast.Node, src []byte, srcs []sourceJSON) {
	byID := map[string]sourceJSON{}
	for _, s := range srcs {
		if s.ID != "" {
			byID[s.ID] = s
		}
	}
	var parents []ast.Node
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.(type) {
		case *ast.Link, *ast.CodeSpan:
			return ast.WalkSkipChildren, nil
		}
		if hasText(n) {
			parents = append(parents, n)
		}
		return ast.WalkContinue, nil
	})
	for _, p := range parents {
		citeRuns(p, src, byID)
	}
}

func hasText(n ast.Node) bool {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if _, ok := c.(*ast.Text); ok {
			return true
		}
	}
	return false
}

// citeRuns handles each run of adjacent text in p, since the parser may
// split one stretch of text into several nodes.
func citeRuns(p ast.Node, src []byte, byID map[string]sourceJSON) {
	for c := p.FirstChild(); c != nil; {
		first, ok := c.(*ast.Text)
		if !ok {
			c = c.NextSibling()
			continue
		}
		last := first
		for {
			next, ok := last.NextSibling().(*ast.Text)
			if !ok || last.SoftLineBreak() || last.HardLineBreak() || next.Segment.Start != last.Segment.Stop {
				break
			}
			last = next
		}
		c = last.NextSibling()
		citeRun(p, first, last, src, byID)
	}
}

func citeRun(p ast.Node, first, last *ast.Text, src []byte, byID map[string]sourceJSON) {
	start, stop := first.Segment.Start, last.Segment.Stop
	ms := citeRe.FindAllSubmatchIndex(src[start:stop], -1)
	if ms == nil {
		return
	}
	var nodes []ast.Node
	at := start
	for _, m := range ms {
		if at < start+m[0] {
			nodes = append(nodes, ast.NewTextSegment(text.NewSegment(at, start+m[0])))
		}
		n := &citeNode{}
		for id := range strings.SplitSeq(string(src[start+m[2]:start+m[3]]), ",") {
			id = strings.TrimSpace(id)
			s, ok := byID[id]
			if !ok {
				s = sourceJSON{ID: id, Broken: true}
			}
			n.Sources = append(n.Sources, s)
		}
		nodes = append(nodes, n)
		at = start + m[1]
	}
	tail := ast.NewTextSegment(text.NewSegment(at, stop))
	tail.SetSoftLineBreak(last.SoftLineBreak())
	tail.SetHardLineBreak(last.HardLineBreak())
	nodes = append(nodes, tail)
	after := last.NextSibling()
	for c := ast.Node(first); c != after; {
		next := c.NextSibling()
		p.RemoveChild(p, c)
		c = next
	}
	for _, n := range nodes {
		if after == nil {
			p.AppendChild(p, n)
		} else {
			p.InsertBefore(p, after, n)
		}
	}
}

type citeRenderer struct{}

func (citeRenderer) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(kindCite, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		_, _ = w.WriteString("（source: ")
		for i, s := range n.(*citeNode).Sources {
			if i > 0 {
				_, _ = w.WriteString(", ")
			}
			id := util.EscapeHTML([]byte(s.ID))
			switch {
			case s.Broken && s.Title == "":
				_, _ = w.WriteString(`<a class="broken" title="Not in sources: ` + string(id) + `">`)
			case s.Broken:
				_, _ = w.WriteString(`<a class="broken" title="Not in the bundle: ` + string(util.EscapeHTML([]byte(s.Resource))) + `">`)
			case s.Href != "":
				_, _ = w.WriteString(`<a href="` + string(util.EscapeHTML(util.URLEscape([]byte(s.Href), true))) + `" title="` + string(util.EscapeHTML([]byte(s.Title))) + `">`)
			default:
				_, _ = w.WriteString(`<a title="` + string(util.EscapeHTML([]byte(s.Title))) + `">`)
			}
			_, _ = w.Write(id)
			_, _ = w.WriteString("</a>")
		}
		_, _ = w.WriteString("）")
		return ast.WalkContinue, nil
	})
}
