package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	ProblemsDir = "problems"
	StagingDir  = "staging"
	AssetsDir   = "assets"
)

var fileRe = regexp.MustCompile(`^P(\d+)\.md$`)

type Repo struct {
	Root string
	Cfg  *Config
}

func Open(dir string) (*Repo, error) {
	root, err := Find(dir)
	if err != nil {
		return nil, err
	}
	cfg, err := LoadConfig(root)
	if err != nil {
		return nil, err
	}
	return &Repo{Root: root, Cfg: cfg}, nil
}

func (r *Repo) Path(parts ...string) string {
	return filepath.Join(append([]string{r.Root}, parts...)...)
}

func (r *Repo) ProblemPath(n int) string { return r.Path(ProblemsDir, FormatID(n)+".md") }
func (r *Repo) StagingPath(n int) string { return r.Path(StagingDir, FormatID(n)+".md") }

func (r *Repo) Numbers(dir string) ([]int, error) {
	ents, err := os.ReadDir(r.Path(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []int
	for _, e := range ents {
		if m := fileRe.FindStringSubmatch(e.Name()); m != nil && !e.IsDir() {
			n, _ := ParseID(m[1])
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out, nil
}

// NextNumber is one past the highest number used in problems/ or staging/.
func (r *Repo) NextNumber() (int, error) {
	max := 0
	for _, d := range []string{ProblemsDir, StagingDir} {
		ns, err := r.Numbers(d)
		if err != nil {
			return 0, err
		}
		if len(ns) > 0 && ns[len(ns)-1] > max {
			max = ns[len(ns)-1]
		}
	}
	return max + 1, nil
}

func loadFile(path string, n int) (*Problem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	p.ID = FormatID(n)
	return p, nil
}

func (r *Repo) Load(n int) (*Problem, error)       { return loadFile(r.ProblemPath(n), n) }
func (r *Repo) LoadStaged(n int) (*Problem, error) { return loadFile(r.StagingPath(n), n) }

func (r *Repo) LoadAll() ([]*Problem, error) {
	ns, err := r.Numbers(ProblemsDir)
	if err != nil {
		return nil, err
	}
	out := make([]*Problem, 0, len(ns))
	for _, n := range ns {
		p, err := r.Load(n)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func writeFile(path string, p *Problem, draft bool) error {
	data, err := p.Marshal(draft)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (r *Repo) WriteProblem(p *Problem) error { return writeFile(r.ProblemPath(p.Number()), p, false) }
func (r *Repo) WriteStaged(p *Problem) error  { return writeFile(r.StagingPath(p.Number()), p, true) }

// Normalize rewrites metadata into canonical form and lists every violation.
func (r *Repo) Normalize(p *Problem) []string {
	var errs []string
	m := &p.Meta
	m.Title = strings.TrimSpace(m.Title)
	if m.Title == "" {
		errs = append(errs, "title 不能为空")
	}
	if s, ok := r.Cfg.Subject(m.Subject); ok {
		m.Subject = s.ID
	} else {
		errs = append(errs, fmt.Sprintf("subject %q 不在配置中，可选: %s（新增: errata subject add）", m.Subject, Describe(r.Cfg.Subjects)))
	}
	if t, ok := r.Cfg.Type(m.Type); ok {
		m.Type = t.ID
	} else {
		errs = append(errs, fmt.Sprintf("type %q 不在配置中，可选: %s（新增: errata type add）", m.Type, Describe(r.Cfg.Types)))
	}
	seen := map[string]bool{}
	var tags []string
	for _, t := range m.Tags {
		if strings.TrimSpace(t) == "" {
			continue
		}
		n, ok := NormTag(t)
		if !ok {
			errs = append(errs, fmt.Sprintf("tag %q 必须是英文 kebab-case（如 quadratic-function）", t))
			continue
		}
		if !seen[n] {
			seen[n] = true
			tags = append(tags, n)
		}
	}
	m.Tags = tags
	if len(tags) == 0 {
		errs = append(errs, "至少需要一个 tag")
	}
	if m.Difficulty < 0 || m.Difficulty > 5 {
		errs = append(errs, "difficulty 应在 0-5 之间")
	}
	if strings.TrimSpace(p.Sections[SecStem]) == "" {
		errs = append(errs, "题干为空")
	}
	return errs
}

func (r *Repo) Commit(p *Problem) error {
	if errs := r.Normalize(p); len(errs) > 0 {
		return fmt.Errorf("%s 校验失败:\n  - %s", p.ID, strings.Join(errs, "\n  - "))
	}
	if p.Meta.Created == "" {
		p.Meta.Created = time.Now().Format("2006-01-02")
	}
	return r.WriteProblem(p)
}

func (r *Repo) Remove(n int) (bool, error) {
	removed := false
	for _, path := range []string{r.ProblemPath(n), r.StagingPath(n)} {
		err := os.Remove(path)
		if err == nil {
			removed = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
	}
	return removed, nil
}

func Tags(ps []*Problem) map[string]int {
	out := map[string]int{}
	for _, p := range ps {
		for _, t := range p.Meta.Tags {
			out[t]++
		}
	}
	return out
}

func SortedKeys(m map[string]int) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
