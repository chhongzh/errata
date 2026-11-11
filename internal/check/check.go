package check

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chhongzh/errata/internal/derive"
	"github.com/chhongzh/errata/internal/gitx"
	"github.com/chhongzh/errata/internal/store"
)

type Level string

const (
	Error Level = "error"
	Warn  Level = "warn"
)

type Issue struct {
	Level Level  `json:"level"`
	Path  string `json:"path"`
	Msg   string `json:"msg"`
}

func (i Issue) String() string { return fmt.Sprintf("%-5s %s: %s", i.Level, i.Path, i.Msg) }

type Options struct {
	Clean bool
}

func Run(r *store.Repo, opt Options) ([]Issue, error) {
	var out []Issue
	add := func(l Level, path, format string, a ...any) {
		out = append(out, Issue{l, path, fmt.Sprintf(format, a...)})
	}

	var good []*store.Problem
	ents, err := os.ReadDir(r.Path(store.ProblemsDir))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range ents {
		rel := store.ProblemsDir + "/" + e.Name()
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			add(Error, rel, "problems/ 下只允许 P<n>.md")
			continue
		}
		n, err := store.ParseID(strings.TrimSuffix(e.Name(), ".md"))
		if err != nil || store.FormatID(n)+".md" != e.Name() {
			add(Error, rel, "文件名应为 P<n>.md（n 为无前导零的正整数）")
			continue
		}
		raw, err := os.ReadFile(r.Path(rel))
		if err != nil {
			return nil, err
		}
		p, err := store.Parse(raw)
		if err != nil {
			add(Error, rel, "%v", err)
			continue
		}
		p.ID = store.FormatID(n)
		if errs := r.Normalize(p); len(errs) > 0 {
			for _, m := range errs {
				add(Error, rel, "%s", m)
			}
			continue
		}
		canon, err := p.Marshal(false)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(canon, raw) {
			add(Error, rel, "格式不规范（运行 errata fmt 修复）")
		}
		good = append(good, p)
	}
	sort.Slice(good, func(i, j int) bool { return good[i].Number() < good[j].Number() })

	out = append(out, checkLinks(r, good)...)

	if want, err := derive.Readme(r, good); err == nil {
		if got, err := os.ReadFile(r.Path("README.md")); err != nil || !bytes.Equal(got, want) {
			add(Error, "README.md", "与模板/题库不一致（运行 errata build 或 errata fmt）")
		}
	} else {
		add(Error, ".errata/"+store.ReadmeTmpl, "%v", err)
	}
	if files, err := derive.SiteFiles(r, good); err == nil {
		dir := r.Cfg.Site.Dir
		for name, want := range files {
			if got, err := os.ReadFile(r.Path(dir, name)); err != nil || !bytes.Equal(got, want) {
				add(Error, dir+"/"+name, "站点文件过期（运行 errata build）")
				break
			}
		}
	}

	if ns, _ := r.Numbers(store.StagingDir); len(ns) > 0 {
		var ids []string
		for _, n := range ns {
			ids = append(ids, store.FormatID(n))
		}
		add(Warn, store.StagingDir, "有未提交的草稿: %s", strings.Join(ids, " "))
	}
	if ns, _ := r.Numbers(store.StagingDir); len(ns) > 0 {
		for _, n := range ns {
			if _, err := os.Stat(r.ProblemPath(n)); err == nil {
				add(Error, store.StagingDir+"/"+store.FormatID(n)+".md", "编号已被 problems/ 使用")
			}
		}
	}

	if opt.Clean {
		g, err := gitx.Open(r.Root)
		if err != nil {
			add(Error, ".git", "不是 Git 仓库: %v", err)
		} else if dirty, err := g.Dirty(); err != nil {
			return nil, err
		} else {
			for _, d := range dirty {
				add(Error, strings.TrimSpace(d[3:]), "工作区未提交 (%s)", strings.TrimSpace(d[:2]))
			}
		}
	}
	return out, nil
}

func HasErrors(is []Issue) bool {
	for _, i := range is {
		if i.Level == Error {
			return true
		}
	}
	return false
}

// expectedLinks maps "by-tag/calculus/P1.md" to its link target.
func expectedLinks(ps []*store.Problem) map[string]string {
	m := map[string]string{}
	put := func(dir, name string, p *store.Problem) {
		name = store.DirName(name)
		if name != "" {
			m[dir+"/"+name+"/"+p.ID+".md"] = filepath.Join("..", "..", store.ProblemsDir, p.ID+".md")
		}
	}
	for _, p := range ps {
		put("by-subject", p.Meta.Subject, p)
		put("by-type", p.Meta.Type, p)
		for _, t := range p.Meta.Tags {
			put("by-tag", t, p)
		}
	}
	return m
}

func checkLinks(r *store.Repo, ps []*store.Problem) []Issue {
	var out []Issue
	want := expectedLinks(ps)
	found := map[string]bool{}
	for _, d := range store.LinkDirs {
		root := r.Path(d)
		_ = filepath.WalkDir(root, func(path string, de os.DirEntry, err error) error {
			if err != nil || path == root {
				return nil
			}
			rel, _ := filepath.Rel(r.Root, path)
			rel = filepath.ToSlash(rel)
			if de.IsDir() {
				if strings.Count(rel, "/") > 1 {
					out = append(out, Issue{Error, rel, "分类目录下不应再有子目录"})
				}
				return nil
			}
			found[rel] = true
			exp, ok := want[rel]
			if !ok {
				out = append(out, Issue{Error, rel, "多余的链接（对应题目不存在，或其分类/标签已变更）"})
				return nil
			}
			if de.Type()&os.ModeSymlink == 0 {
				out = append(out, Issue{Error, rel, "应为软链接，实际是普通文件"})
				return nil
			}
			tgt, err := os.Readlink(path)
			if err != nil {
				return nil
			}
			if filepath.ToSlash(tgt) != filepath.ToSlash(exp) {
				out = append(out, Issue{Error, rel, fmt.Sprintf("软链接指向 %q，应为 %q", tgt, exp)})
			} else if _, err := os.Stat(path); err != nil {
				out = append(out, Issue{Error, rel, "软链接已悬空"})
			}
			return nil
		})
	}
	var missing []string
	for rel := range want {
		if !found[rel] {
			missing = append(missing, rel)
		}
	}
	sort.Strings(missing)
	for _, rel := range missing {
		out = append(out, Issue{Error, rel, "缺少软链接"})
	}
	return out
}

// Format rewrites every problem in canonical form, then rebuilds the derived files.
func Format(r *store.Repo) (changed []string, problems []Issue, err error) {
	ents, err := os.ReadDir(r.Path(store.ProblemsDir))
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	for _, e := range ents {
		rel := store.ProblemsDir + "/" + e.Name()
		n, perr := store.ParseID(strings.TrimSuffix(e.Name(), ".md"))
		if e.IsDir() || perr != nil || !strings.HasSuffix(e.Name(), ".md") {
			problems = append(problems, Issue{Error, rel, "无法识别的文件，已跳过"})
			continue
		}
		raw, err := os.ReadFile(r.Path(rel))
		if err != nil {
			return nil, nil, err
		}
		p, err := store.Parse(raw)
		if err != nil {
			problems = append(problems, Issue{Error, rel, err.Error()})
			continue
		}
		p.ID = store.FormatID(n)
		if errs := r.Normalize(p); len(errs) > 0 {
			for _, m := range errs {
				problems = append(problems, Issue{Error, rel, m})
			}
			continue
		}
		canon, err := p.Marshal(false)
		if err != nil {
			return nil, nil, err
		}
		if !bytes.Equal(canon, raw) {
			if err := os.WriteFile(r.Path(rel), canon, 0o644); err != nil {
				return nil, nil, err
			}
			changed = append(changed, rel)
		}
	}
	if len(problems) > 0 {
		return changed, problems, nil
	}
	return changed, nil, derive.Build(r)
}
