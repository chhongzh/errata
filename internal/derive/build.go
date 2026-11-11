package derive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/chhongzh/errata/internal/assets"
	"github.com/chhongzh/errata/internal/render"
	"github.com/chhongzh/errata/internal/store"
)

type Entry struct {
	Name  string
	Dir   string
	Count int
}

type readmeData struct {
	Title    string
	Total    int
	Subjects []Entry
	Types    []Entry
	Tags     []Entry
	Recent   []RecentItem
}

type RecentItem struct {
	ID, Title, Created  string
	Subject, SubjectDir string
	Type, TypeDir       string
}

func tmplPath(r *store.Repo) string { return r.Path(store.MetaDir, store.ReadmeTmpl) }

func EnsureTemplate(r *store.Repo) error {
	if _, err := os.Stat(tmplPath(r)); err == nil {
		return nil
	}
	return os.WriteFile(tmplPath(r), []byte(assets.ReadmeTemplate), 0o644)
}

func countBy(ps []*store.Problem, keys func(*store.Problem) []string, order []string, label func(string) string) []Entry {
	m := map[string]int{}
	for _, p := range ps {
		for _, k := range keys(p) {
			m[k]++
		}
	}
	var out []Entry
	seen := map[string]bool{}
	for _, k := range order {
		if m[k] > 0 {
			out = append(out, Entry{label(k), store.DirName(k), m[k]})
			seen[k] = true
		}
	}
	var rest []string
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		out = append(out, Entry{label(k), store.DirName(k), m[k]})
	}
	return out
}

func Readme(r *store.Repo, ps []*store.Problem) ([]byte, error) {
	src, err := os.ReadFile(tmplPath(r))
	if err != nil {
		src = []byte(assets.ReadmeTemplate)
	}
	t, err := template.New("readme").Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("README 模板解析失败: %w", err)
	}
	d := readmeData{Title: r.Cfg.Title, Total: len(ps)}
	same := func(s string) string { return s }
	d.Subjects = countBy(ps, func(p *store.Problem) []string { return []string{p.Meta.Subject} }, ids(r.Cfg.Subjects), r.Cfg.SubjectLabel)
	d.Types = countBy(ps, func(p *store.Problem) []string { return []string{p.Meta.Type} }, ids(r.Cfg.Types), r.Cfg.TypeLabel)
	d.Tags = countBy(ps, func(p *store.Problem) []string { return p.Meta.Tags }, nil, same)
	sort.SliceStable(d.Tags, func(i, j int) bool { return d.Tags[i].Count > d.Tags[j].Count })
	for i := len(ps) - 1; i >= 0 && len(d.Recent) < 10; i-- {
		p := ps[i]
		d.Recent = append(d.Recent, RecentItem{
			ID: p.ID, Title: p.Meta.Title, Created: p.Meta.Created,
			Subject: r.Cfg.SubjectLabel(p.Meta.Subject), SubjectDir: store.DirName(p.Meta.Subject),
			Type: r.Cfg.TypeLabel(p.Meta.Type), TypeDir: store.DirName(p.Meta.Type),
		})
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return nil, fmt.Errorf("README 模板渲染失败: %w", err)
	}
	return buf.Bytes(), nil
}

type siteProblem struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Subject    string   `json:"subject"`
	Type       string   `json:"type"`
	Tags       []string `json:"tags"`
	Source     string   `json:"source"`
	Difficulty int      `json:"difficulty"`
	Created    string   `json:"created"`
	StemText   string   `json:"stem_text"`
	Stem       string   `json:"stem"`
	Answer     string   `json:"answer"`
	Solution   string   `json:"solution"`
}

type siteData struct {
	Title    string        `json:"title"`
	Subjects []store.Named `json:"subjects"`
	Types    []store.Named `json:"types"`
	Problems []siteProblem `json:"problems"`
}

func ids(list []store.Named) []string {
	out := make([]string, len(list))
	for i, n := range list {
		out[i] = n.ID
	}
	return out
}

func SiteFiles(r *store.Repo, ps []*store.Problem) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(assets.Site, "site", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := assets.Site.ReadFile(path)
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(path, "site/")
		if name == "index.html" {
			b = bytes.ReplaceAll(b, []byte("{{TITLE}}"), []byte(escapeHTML(r.Cfg.Title)))
		}
		out[name] = b
		return nil
	})
	if err != nil {
		return nil, err
	}

	data := siteData{Title: r.Cfg.Title, Subjects: r.Cfg.Subjects, Types: r.Cfg.Types, Problems: []siteProblem{}}
	for i := len(ps) - 1; i >= 0; i-- {
		p := ps[i]
		sp := siteProblem{
			ID: p.ID, Title: p.Meta.Title, Subject: p.Meta.Subject, Type: p.Meta.Type, Tags: p.Meta.Tags,
			Source: p.Meta.Source, Difficulty: p.Meta.Difficulty, Created: p.Meta.Created,
			StemText: p.Sections[store.SecStem],
		}
		if sp.Tags == nil {
			sp.Tags = []string{}
		}
		for _, f := range []struct {
			name string
			dst  *string
		}{{store.SecStem, &sp.Stem}, {store.SecAnswer, &sp.Answer}, {store.SecSolution, &sp.Solution}} {
			if src := p.Sections[f.name]; src != "" {
				h, err := render.Markdown(src)
				if err != nil {
					return nil, fmt.Errorf("%s %s: %w", p.ID, f.name, err)
				}
				*f.dst = strings.ReplaceAll(h, `src="../assets/`, `src="assets/`)
			}
		}
		data.Problems = append(data.Problems, sp)
	}
	js, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	// "</" would end the inline script context if data.js is ever inlined.
	js = bytes.ReplaceAll(js, []byte("</"), []byte(`<\/`))
	out["data.js"] = append([]byte("window.ERRATA = "), append(js, ';', '\n')...)
	out[".nojekyll"] = []byte{}
	if err := copyAssets(r, out); err != nil {
		return nil, err
	}
	return out, nil
}

func escapeHTML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// Build regenerates the symlink trees, README.md and the site. problems/ and staging/ are never touched.
func Build(r *store.Repo) error {
	ps, err := r.LoadAll()
	if err != nil {
		return err
	}
	if err := EnsureTemplate(r); err != nil {
		return err
	}
	if err := r.RebuildLinks(ps); err != nil {
		return err
	}
	readme, err := Readme(r, ps)
	if err != nil {
		return err
	}
	if err := os.WriteFile(r.Path("README.md"), readme, 0o644); err != nil {
		return err
	}
	files, err := SiteFiles(r, ps)
	if err != nil {
		return err
	}
	dir := r.Path(r.Cfg.Site.Dir)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for name, b := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// copyAssets adds problem images to the site so `assets/x.png` resolves when docs/ is the web root.
func copyAssets(r *store.Repo, files map[string][]byte) error {
	ents, err := os.ReadDir(r.Path(store.AssetsDir))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b, err := os.ReadFile(r.Path(store.AssetsDir, e.Name()))
		if err != nil {
			return err
		}
		files["assets/"+e.Name()] = b
	}
	return nil
}
