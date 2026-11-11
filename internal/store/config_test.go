package store

import "testing"

func TestNormTag(t *testing.T) {
	for in, want := range map[string]string{
		"Quadratic Function": "quadratic-function",
		"case_analysis":      "case-analysis",
		"  extremum ":        "extremum",
		"a--b":               "a-b",
	} {
		if got, ok := NormTag(in); !ok || got != want {
			t.Errorf("NormTag(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"二次函数", "a/b", "", "-"} {
		if _, ok := NormTag(in); ok {
			t.Errorf("NormTag(%q) should be rejected", in)
		}
	}
}

func TestResolve(t *testing.T) {
	c := DefaultConfig("")
	c.Subjects[1].Aliases = []string{"maths"}
	for _, in := range []string{"math", "数学", "MATH", "maths"} {
		if s, ok := c.Subject(in); !ok || s.ID != "math" {
			t.Errorf("Subject(%q) = %v, %v", in, s, ok)
		}
	}
	if _, ok := c.Type("nope"); ok {
		t.Error("unknown type resolved")
	}
}
