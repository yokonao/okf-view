package okf

import (
	"slices"
	"testing"
)

func TestSiblingLinks(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"dev/a.md":         "---\ntype: X\n---\n[acc](../kakeibo/acc/x.md) [root](../kakeibo/) [gone](../kakeibo/gone.md) [far](../../kakeibo/acc/x.md)\n",
		"dev/sub/b.md":     "---\ntype: X\n---\n[from concept](../../kakeibo/acc/x.md) [from root](../kakeibo/acc/x.md) [self](../dev/a.md)\n",
		"kakeibo/acc/x.md": "---\ntype: X\n---\n",
	})
	bs, err := Load(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	dev, kakeibo := bs[0], bs[1]
	for _, tt := range []struct {
		from, target, bundle, rel string
	}{
		{"a", "../kakeibo/acc/x.md", "kakeibo", "acc/x.md"},
		{"a", "../kakeibo/", "kakeibo", ""},
		{"a", "../kakeibo/gone.md", "", ""},
		{"a", "../../kakeibo/acc/x.md", "", ""},
		{"a", "/../kakeibo/acc/x.md", "", ""},
		{"sub/b", "../../kakeibo/acc/x.md", "kakeibo", "acc/x.md"},
		{"sub/b", "../kakeibo/acc/x.md", "kakeibo", "acc/x.md"},
		{"sub/b", "../dev/a.md", "", ""},
	} {
		o, rel, ok := dev.ResolveSibling(tt.from, tt.target)
		name := ""
		if ok {
			name = o.Name
		}
		if name != tt.bundle || rel != tt.rel || ok != (tt.bundle != "") {
			t.Errorf("ResolveSibling(%q, %q) = %q, %q, %v; want %q, %q", tt.from, tt.target, name, rel, ok, tt.bundle, tt.rel)
		}
	}
	want := []Ref{{"dev", "a"}, {"dev", "sub/b"}}
	if got := kakeibo.SiblingBacklinks["acc/x"]; !slices.Equal(got, want) {
		t.Errorf("sibling backlinks = %v, want %v", got, want)
	}
	if len(dev.SiblingBacklinks) != 0 {
		t.Errorf("dev sibling backlinks = %v", dev.SiblingBacklinks)
	}
}
