package render

import (
	"strings"
	"testing"
)

func TestMarkdownKeepsMath(t *testing.T) {
	cases := []struct{ in, want string }{
		{`设 $a_1*b_2$ 与 $x_i$`, `$a_1*b_2$`},
		{"$$\\begin{cases}a\\\\b\\end{cases}$$", `\begin{cases}a\\b\end{cases}`},
		{`\(x_1\) 和 \[y_2\]`, `\(x_1\)`},
		{"`$not math_a_b$`", `<code>$not math_a_b$</code>`},
		{"价格 $5 和 $6", `$5 和 $6`},
	}
	for _, c := range cases {
		got, err := Markdown(c.in)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("Markdown(%q) = %q, want it to contain %q", c.in, got, c.want)
		}
		if strings.Contains(got, "ERRATAMATH") {
			t.Errorf("placeholder leaked: %q", got)
		}
	}
}

func TestMarkdownManyFormulas(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 15; i++ {
		b.WriteString("$x_1$ ")
	}
	got, _ := Markdown(b.String())
	if strings.Count(got, "$x_1$") != 15 {
		t.Errorf("got %q", got)
	}
}
