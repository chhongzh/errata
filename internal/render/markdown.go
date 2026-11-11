package render

import (
	"bytes"
	"fmt"
	"html"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(gmhtml.WithHardWraps()),
)

// protectMath swaps LaTeX for placeholders so Markdown cannot mangle `_`, `*` or `\\` inside formulas. Code spans and fences are skipped.
func protectMath(src string) (string, []string) {
	var (
		out   strings.Builder
		maths []string
	)
	put := func(s string) {
		maths = append(maths, s)
		fmt.Fprintf(&out, "ERRATAMATH%dX", len(maths)-1)
	}

	lines := strings.Split(src, "\n")
	var buf strings.Builder // text outside fences, processed as one run
	flush := func() {
		scanInline(buf.String(), &out, put)
		buf.Reset()
	}
	fenced := false
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		isFence := strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~")
		switch {
		case isFence && !fenced:
			flush()
			fenced = true
			out.WriteString(line)
		case isFence && fenced:
			fenced = false
			out.WriteString(line)
		case fenced:
			out.WriteString(line)
		default:
			buf.WriteString(line)
		}
		if i < len(lines)-1 {
			if fenced || isFence {
				out.WriteString("\n")
			} else {
				buf.WriteString("\n")
			}
		}
	}
	flush()
	return out.String(), maths
}

func scanInline(s string, out *strings.Builder, put func(string)) {
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s):
			// \( ... \) and \[ ... \]
			if s[i+1] == '(' || s[i+1] == '[' {
				closer := `\)`
				if s[i+1] == '[' {
					closer = `\]`
				}
				if j := strings.Index(s[i+2:], closer); j >= 0 {
					end := i + 2 + j + 2
					put(s[i:end])
					i = end
					continue
				}
			}
			out.WriteString(s[i : i+2])
			i += 2
		case c == '`':
			n := 0
			for i+n < len(s) && s[i+n] == '`' {
				n++
			}
			fence := strings.Repeat("`", n)
			if j := strings.Index(s[i+n:], fence); j >= 0 {
				end := i + n + j + n
				out.WriteString(s[i:end])
				i = end
				continue
			}
			out.WriteString(fence)
			i += n
		case c == '$':
			if strings.HasPrefix(s[i:], "$$") {
				if j := strings.Index(s[i+2:], "$$"); j >= 0 {
					end := i + 2 + j + 2
					put(s[i:end])
					i = end
					continue
				}
				out.WriteString("$$")
				i += 2
				continue
			}
			if j := inlineEnd(s, i+1); j >= 0 {
				put(s[i : j+1])
				i = j + 1
				continue
			}
			out.WriteByte(c)
			i++
		default:
			out.WriteByte(c)
			i++
		}
	}
}

func inlineEnd(s string, start int) int {
	if start >= len(s) || s[start] == ' ' || s[start] == '\n' {
		return -1
	}
	for j := start; j < len(s); j++ {
		switch s[j] {
		case '\n':
			return -1
		case '\\':
			j++
		case '$':
			if s[j-1] == ' ' {
				return -1
			}
			return j
		}
	}
	return -1
}

// Markdown renders HTML with LaTeX kept verbatim for KaTeX auto-render.
func Markdown(src string) (string, error) {
	protected, maths := protectMath(src)
	var buf bytes.Buffer
	if err := md.Convert([]byte(protected), &buf); err != nil {
		return "", err
	}
	res := buf.String()
	for i := len(maths) - 1; i >= 0; i-- {
		res = strings.ReplaceAll(res, fmt.Sprintf("ERRATAMATH%dX", i), html.EscapeString(maths[i]))
	}
	return res, nil
}
