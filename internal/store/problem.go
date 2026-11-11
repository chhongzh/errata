package store

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Section names as they appear in problem files (level-2 headings). Only the
// correct answer and the correct steps are kept: no wrong answers, no notes or
// summary columns.
const (
	SecStem     = "题干"
	SecAnswer   = "答案"
	SecSolution = "解析"
)

var SectionOrder = []string{SecStem, SecAnswer, SecSolution}

type Meta struct {
	Title      string   `yaml:"title"`
	Subject    string   `yaml:"subject"`
	Type       string   `yaml:"type"`
	Tags       []string `yaml:"tags,flow"`
	Source     string   `yaml:"source,omitempty"`
	Difficulty int      `yaml:"difficulty,omitempty"`
	Created    string   `yaml:"created,omitempty"`
}

type Problem struct {
	ID       string // "P12"; derived from the file name, never stored in the file
	Meta     Meta
	Sections map[string]string
}

// Doc is the flat JSON form exchanged with AI agents and the web editor.
type Doc struct {
	ID         string   `json:"id,omitempty"`
	Title      string   `json:"title"`
	Subject    string   `json:"subject"`
	Type       string   `json:"type"`
	Tags       []string `json:"tags"`
	Source     string   `json:"source,omitempty"`
	Difficulty int      `json:"difficulty,omitempty"`
	Created    string   `json:"created,omitempty"`
	Stem       string   `json:"stem,omitempty"`
	Answer     string   `json:"answer,omitempty"`
	Solution   string   `json:"solution,omitempty"`
}

func (p *Problem) Doc() Doc {
	s := p.Sections
	tags := p.Meta.Tags
	if tags == nil {
		tags = []string{}
	}
	return Doc{
		ID: p.ID, Title: p.Meta.Title, Subject: p.Meta.Subject, Type: p.Meta.Type, Tags: tags,
		Source: p.Meta.Source, Difficulty: p.Meta.Difficulty, Created: p.Meta.Created,
		Stem: s[SecStem], Answer: s[SecAnswer], Solution: s[SecSolution],
	}
}

func (d Doc) Problem() *Problem {
	secs := map[string]string{}
	for name, v := range map[string]string{
		SecStem: d.Stem, SecAnswer: d.Answer, SecSolution: d.Solution,
	} {
		if t := strings.TrimSpace(v); t != "" {
			secs[name] = t
		}
	}
	return &Problem{
		ID: d.ID,
		Meta: Meta{
			Title: d.Title, Subject: d.Subject, Type: d.Type, Tags: d.Tags,
			Source: d.Source, Difficulty: d.Difficulty, Created: d.Created,
		},
		Sections: secs,
	}
}

var idRe = regexp.MustCompile(`^[pP]?(\d+)$`)

func FormatID(n int) string { return "P" + strconv.Itoa(n) }

func ParseID(s string) (int, error) {
	m := idRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("无效的题号 %q（应为 P12 或 12）", s)
	}
	n, _ := strconv.Atoi(m[1])
	if n <= 0 {
		return 0, fmt.Errorf("无效的题号 %q", s)
	}
	return n, nil
}

func (p *Problem) Number() int {
	n, _ := ParseID(p.ID)
	return n
}

func SplitFrontMatter(data []byte) (fm string, body string) {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return "", s
	}
	rest := s[4:]
	if strings.HasPrefix(rest, "---\n") {
		return "", rest[4:]
	}
	if i := strings.Index(rest, "\n---\n"); i >= 0 {
		return rest[:i+1], rest[i+5:]
	}
	if strings.HasSuffix(rest, "\n---") {
		return rest[:len(rest)-4], ""
	}
	return "", s
}

// ParseSections splits a body into its known level-2 sections. A body without
// any `## ` heading is taken as the stem as a whole.
func ParseSections(body string) (map[string][]string, []string, error) {
	known := map[string]bool{}
	for _, n := range SectionOrder {
		known[n] = true
	}
	secs := map[string][]string{}
	cur := ""
	fenced := false
	var pre []string
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			fenced = !fenced
		}
		if !fenced && strings.HasPrefix(line, "## ") {
			name := strings.TrimSpace(line[3:])
			if !known[name] {
				return nil, nil, fmt.Errorf("未知章节 %q；允许的二级标题: %s（正文内部请用 ### 及以下）", "## "+name, strings.Join(SectionOrder, " / "))
			}
			if _, dup := secs[name]; dup {
				return nil, nil, fmt.Errorf("章节 %q 重复", name)
			}
			cur = name
			secs[cur] = []string{}
			continue
		}
		if cur == "" {
			pre = append(pre, line)
		} else {
			secs[cur] = append(secs[cur], line)
		}
	}
	return secs, pre, nil
}

func parseBody(body string) (map[string]string, error) {
	secs, pre, err := ParseSections(body)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	preText := strings.TrimSpace(strings.Join(pre, "\n"))
	if len(secs) == 0 {
		if preText != "" {
			out[SecStem] = preText
		}
		return out, nil
	}
	if preText != "" {
		return nil, fmt.Errorf("第一个 `## ` 标题之前存在多余文本")
	}
	for k, v := range secs {
		if t := strings.TrimSpace(strings.Join(v, "\n")); t != "" {
			out[k] = t
		}
	}
	return out, nil
}

// Parse leaves ID empty: it comes from the file name.
func Parse(data []byte) (*Problem, error) {
	fm, body := SplitFrontMatter(data)
	p := &Problem{}
	if strings.TrimSpace(fm) != "" {
		if err := yaml.Unmarshal([]byte(fm), &p.Meta); err != nil {
			return nil, fmt.Errorf("front matter: %w", err)
		}
	}
	secs, err := parseBody(body)
	if err != nil {
		return nil, err
	}
	p.Sections = secs
	return p, nil
}

// Marshal renders the problem file. With draft=true, empty sections are kept as
// headings so a human has a skeleton to fill in.
func (p *Problem) Marshal(draft bool) ([]byte, error) {
	var buf bytes.Buffer
	meta := p.Meta
	if meta.Tags == nil {
		meta.Tags = []string{}
	}
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(meta); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	buf.WriteString("---\n\n")
	first := true
	for _, n := range SectionOrder {
		t := strings.TrimSpace(p.Sections[n])
		if t == "" && !draft {
			continue
		}
		if !first {
			buf.WriteString("\n")
		}
		first = false
		buf.WriteString("## " + n + "\n\n")
		if t != "" {
			buf.WriteString(t + "\n")
		}
	}
	return buf.Bytes(), nil
}
