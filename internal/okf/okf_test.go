package okf

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

var now = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)

func loadOne(t *testing.T, dir string) *Bundle {
	t.Helper()
	bundles, err := Load(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 1 {
		t.Fatalf("got %d bundles, want 1", len(bundles))
	}
	return bundles[0]
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "b"), map[string]string{
		"index.md": "---\nokf_version: \"0.2\"\n---\n# B\n",
		"log.md":   "# Log\n",
		"a.md": `---
type: Decision
title: A
tags: [x]
verified: { by: "human:nao", at: 2026-10-01T00:00:00+09:00 }
stale_after: 2026-11-01
---
See [C](/sub/c.md) and [D](sub/d.md) and [x](https://example.com).
`,
		"sub/c.md":   "---\ntype: Reference\nverified: [{ by: ci }]\n---\nBack to [A](../a.md).\n",
		"sub/d.md":   "---\ntitle: no type\n---\n",
		"sub/e.md":   "no frontmatter",
		".hidden.md": "---\ntype: X\n---\n",
	})
	b := loadOne(t, dir)
	if b.Name != "b" || b.OKFVersion != "0.2" {
		t.Errorf("name, version = %q, %q", b.Name, b.OKFVersion)
	}
	ids := make([]string, 0, len(b.Concepts))
	for id := range b.Concepts {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if want := []string{"a", "sub/c", "sub/d", "sub/e"}; !slices.Equal(ids, want) {
		t.Errorf("concepts = %v, want %v", ids, want)
	}
	if b.Logs[""] == nil || b.Indexes[""] == nil {
		t.Error("root log.md or index.md not taken")
	}

	a := b.Concepts["a"]
	if a.Trust != HumanReviewed || !a.Stale || a.Status != "stable" || a.Verified[0].At != "2026-10-01T00:00:00+09:00" {
		t.Errorf("a = %+v", a)
	}
	if c := b.Concepts["sub/c"]; c.Trust != MachineConfirmed || c.Title != "c" || c.Stale {
		t.Errorf("sub/c = %+v", c)
	}
	if d := b.Concepts["sub/d"]; d.Problem == "" || d.Trust != Unverified {
		t.Errorf("sub/d problem = %q, trust = %q", d.Problem, d.Trust)
	}
	if e := b.Concepts["sub/e"]; e.Problem != "no frontmatter block" {
		t.Errorf("sub/e problem = %q", e.Problem)
	}
	if got := b.Backlinks["sub/c"]; !slices.Equal(got, []string{"a"}) {
		t.Errorf("backlinks of sub/c = %v", got)
	}
	if got := b.Backlinks["a"]; !slices.Equal(got, []string{"sub/c"}) {
		t.Errorf("backlinks of a = %v", got)
	}
}

func TestLoadBundlesDir(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"x/a.md": "", "y/b.md": "", "file.md": ""})
	bundles, err := Load(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 2 || bundles[0].Name != "x" || bundles[1].Name != "y" {
		t.Errorf("bundles = %v", bundles)
	}
	if bundles, err := Load(filepath.Join(dir, "missing"), now); err != nil || bundles != nil {
		t.Errorf("missing dir: %v, %v", bundles, err)
	}
}

func TestLoadLinkOutsideBundle(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	write(t, outside, map[string]string{"secret.md": "---\ntype: X\n---\n"})
	write(t, filepath.Join(dir, "b"), map[string]string{"index.md": "", "in/a.md": ""})
	if err := os.Symlink(outside, filepath.Join(dir, "b", "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("in", filepath.Join(dir, "b", "again")); err != nil {
		t.Fatal(err)
	}
	b := loadOne(t, filepath.Join(dir, "b"))
	if b.Concepts["out/secret"] != nil || b.Files["out/secret.md"] {
		t.Error("followed a link out of the bundle")
	}
	if len(b.Skipped) != 2 {
		t.Errorf("skipped = %v", b.Skipped)
	}
}

func TestResolve(t *testing.T) {
	b := &Bundle{
		Files: map[string]bool{"a.md": true, "x/b.md": true, "x/y/c.md": true},
		Dirs:  map[string]bool{"": true, "x": true, "x/y": true},
	}
	for _, tt := range []struct {
		from, target, want string
		ok                 bool
	}{
		{"x/b", "/a.md", "a.md", true},
		{"x/b", "y/c.md#h", "x/y/c.md", true},
		{"x/b", "../a.md", "a.md", true},
		{"x/b", "x/y/c.md", "x/y/c.md", true}, // written from the bundle root
		{"x/b", "/x/y/", "x/y", true},
		{"x/b", "missing.md", "x/missing.md", false},
		{"a", "../../etc/passwd", "", false},
		{"a", "https://example.com", "", false},
		{"a", "#h", "", false},
		{"a", "x/%62.md", "x/b.md", true},
	} {
		got, ok := b.Resolve(tt.from, tt.target)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Resolve(%q, %q) = %q, %v; want %q, %v", tt.from, tt.target, got, ok, tt.want, tt.ok)
		}
	}
}

func TestDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	t.Setenv("OKF_DIR", "~/.okf")
	if got, _ := Dir(""); got != filepath.Join(home, ".okf") {
		t.Errorf("Dir from OKF_DIR = %q", got)
	}
	if got, _ := Dir("/b"); got != "/b" {
		t.Errorf("Dir(/b) = %q", got)
	}
	t.Setenv("OKF_DIR", "")
	if _, err := Dir(""); err == nil {
		t.Error("Dir without OKF_DIR succeeded")
	}
}
