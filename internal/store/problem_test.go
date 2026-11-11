package store

import (
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	src := "---\ntitle: T\nsubject: 数学\ntype: 解答题\ntags: [a, b]\n---\n\n## 题干\n\nq `## 题干` $x$\n\n```\n## 解析\n```\n\n## 答案\n\n1\n"
	p, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Sections[SecStem], "```\n## 解析\n```") {
		t.Fatalf("fenced heading must stay in stem: %q", p.Sections[SecStem])
	}
	out, err := p.Marshal(false)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	out2, _ := p2.Marshal(false)
	if string(out) != string(out2) {
		t.Fatalf("not idempotent:\n%s\n---\n%s", out, out2)
	}
}

func TestUnknownSection(t *testing.T) {
	if _, err := Parse([]byte("---\ntitle: T\n---\n\n## 笔记\n\nx\n")); err == nil {
		t.Fatal("expected error for unknown section")
	}
}

func TestParseID(t *testing.T) {
	for in, want := range map[string]int{"P12": 12, "p3": 3, "7": 7} {
		if n, err := ParseID(in); err != nil || n != want {
			t.Errorf("ParseID(%q) = %d, %v", in, n, err)
		}
	}
	for _, in := range []string{"", "P0", "Px", "P-1"} {
		if _, err := ParseID(in); err == nil {
			t.Errorf("ParseID(%q) should fail", in)
		}
	}
}
