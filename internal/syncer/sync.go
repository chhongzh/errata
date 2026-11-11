package syncer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"gopkg.in/yaml.v3"

	"github.com/chhongzh/errata/internal/derive"
	"github.com/chhongzh/errata/internal/gitx"
	"github.com/chhongzh/errata/internal/store"
)

// IsSource marks the paths that carry authored content. Everything else is derived and rebuilt after a merge.
func IsSource(path string) bool {
	return strings.HasPrefix(path, store.ProblemsDir+"/") ||
		strings.HasPrefix(path, store.AssetsDir+"/") ||
		strings.HasPrefix(path, store.MetaDir+"/") ||
		path == ".gitignore"
}

const GitIgnore = "# errata: staging 是本地草稿区，不入库\nstaging/\n.DS_Store\n"

type Result struct {
	Log []string
}

func (r *Result) logf(format string, a ...any) { r.Log = append(r.Log, fmt.Sprintf(format, a...)) }

type Options struct {
	Message string
	NoPush  bool
	Offline bool
}

func autoMessage(dirty []string) string {
	var add, mod, del []string
	for _, d := range dirty {
		if len(d) < 4 {
			continue
		}
		code, path := d[:2], d[3:]
		if !strings.HasPrefix(path, store.ProblemsDir+"/") || !strings.HasSuffix(path, ".md") {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(path), ".md")
		switch {
		case strings.Contains(code, "D"):
			del = append(del, id)
		case strings.Contains(code, "?") || strings.Contains(code, "A"):
			add = append(add, id)
		default:
			mod = append(mod, id)
		}
	}
	var parts []string
	for _, g := range []struct {
		verb string
		ids  []string
	}{{"add", add}, {"update", mod}, {"remove", del}} {
		if len(g.ids) > 0 {
			sort.Strings(g.ids)
			parts = append(parts, g.verb+" "+strings.Join(g.ids, ", "))
		}
	}
	if len(parts) == 0 {
		return "errata: sync"
	}
	return "errata: " + strings.Join(parts, "; ")
}

func Sync(repo *store.Repo, opt Options) (*Result, error) {
	res := &Result{}
	g, err := gitx.Open(repo.Root)
	if err != nil {
		return nil, fmt.Errorf("不是 Git 仓库: %w", err)
	}
	if err := derive.Build(repo); err != nil {
		return nil, err
	}
	dirty, err := g.Dirty()
	if err != nil {
		return nil, err
	}
	msg := opt.Message
	if msg == "" {
		msg = autoMessage(dirty)
	}
	committed, err := g.CommitAll(msg)
	if err != nil {
		return nil, err
	}
	if committed {
		res.logf("已提交: %s", msg)
	} else {
		res.logf("本地无变更")
	}
	if opt.Offline || g.RemoteURL() == "" {
		if g.RemoteURL() == "" {
			res.logf("未配置远程（errata remote <url>），仅本地提交")
		}
		return res, nil
	}

	if err := g.Fetch(); err != nil {
		return res, fmt.Errorf("fetch 失败: %w", err)
	}
	div, loc, rem, err := g.Compare()
	if err != nil {
		return res, err
	}
	switch div {
	case gitx.InSync:
		res.logf("已与远程同步")
	case gitx.LocalAhead, gitx.NoRemoteBranch:
		res.logf("本地领先，将推送")
	case gitx.RemoteAhead, gitx.NoLocalCommits:
		if err := g.FastForward(rem); err != nil {
			return res, err
		}
		res.logf("已快进到远程 %s", rem.Hash.String()[:7])
		if err := rebuildAfterPull(repo, g, res); err != nil {
			return res, err
		}
	case gitx.Diverged:
		if err := merge(repo, g, loc, rem, res); err != nil {
			return res, err
		}
	}
	if opt.NoPush {
		return res, nil
	}
	after, _, _, err := g.Compare()
	if err != nil {
		return res, err
	}
	if after == gitx.InSync || after == gitx.RemoteAhead {
		return res, nil
	}
	if err := g.Push(); err != nil {
		return res, fmt.Errorf("push 失败: %w", err)
	}
	res.logf("已推送到 %s/%s", gitx.Remote, g.Branch())
	return res, nil
}

func rebuildAfterPull(repo *store.Repo, g *gitx.Git, res *Result) error {
	cfg, err := store.LoadConfig(repo.Root)
	if err != nil {
		return err
	}
	repo.Cfg = cfg
	if err := derive.Build(repo); err != nil {
		return err
	}
	ok, err := g.CommitAll("errata: rebuild")
	if err != nil {
		return err
	}
	if ok {
		res.logf("重新生成了派生文件并提交")
	}
	return nil
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

func writeAt(root, rel, content string) error {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func readAt(root, rel string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	return string(b), err == nil
}

func problemNumber(path string) (int, bool) {
	if !strings.HasPrefix(path, store.ProblemsDir+"/") {
		return 0, false
	}
	n, err := store.ParseID(strings.TrimSuffix(filepath.Base(path), ".md"))
	return n, err == nil
}

// merge performs a file-level three-way merge of source files. Problems never
// lose data: a collision moves the local copy to a fresh number.
func merge(repo *store.Repo, g *gitx.Git, loc, rem *object.Commit, res *Result) error {
	base, err := gitx.MergeBase(loc, rem)
	if err != nil {
		return err
	}
	remoteCh, err := gitx.Diff(base, rem, IsSource)
	if err != nil {
		return err
	}
	root := repo.Root
	res.logf("本地与远程已分叉，按文件合并（base %s）", base.Hash.String()[:7])

	next, err := maxNumber(repo, rem)
	if err != nil {
		return err
	}
	moveLocal := func(path string) error {
		n, _ := problemNumber(path)
		content, _ := readAt(root, path)
		p, perr := store.Parse([]byte(content))
		next++
		newPath := fmt.Sprintf("%s/%s.md", store.ProblemsDir, store.FormatID(next))
		if perr == nil {
			p.Meta.Title += "（冲突副本）"
			if data, merr := p.Marshal(false); merr == nil {
				content = string(data)
			}
		}
		if err := writeAt(root, newPath, content); err != nil {
			return err
		}
		res.logf("  冲突: %s 两端都有修改，远程版本保留为 P%d，你的版本另存为 %s", store.FormatID(n), n, store.FormatID(next))
		return nil
	}

	apply := func(path, remote string, added bool) error {
		local, has := readAt(root, path)
		baseContent, inBase := gitx.FileAt(base, path)
		switch {
		case has && local == remote:
			return nil
		case path == store.MetaDir+"/"+store.ConfigFile && has:
			merged, err := mergeConfig(local, remote)
			if err != nil {
				return err
			}
			res.logf("  合并配置 %s", path)
			return writeAt(root, path, merged)
		case !has:
			return writeAt(root, path, remote)
		case inBase && local == baseContent:
			return writeAt(root, path, remote)
		}
		// Both sides changed (or both added) differently.
		if _, isProblem := problemNumber(path); isProblem {
			if err := moveLocal(path); err != nil {
				return err
			}
			return writeAt(root, path, remote)
		}
		res.logf("  冲突: %s 两端都有修改，保留本地版本", path)
		return nil
	}

	for path, c := range remoteCh.Added {
		if err := apply(path, c, true); err != nil {
			return err
		}
	}
	for path, c := range remoteCh.Modified {
		if err := apply(path, c, false); err != nil {
			return err
		}
	}
	for _, path := range remoteCh.Deleted {
		local, has := readAt(root, path)
		baseContent, _ := gitx.FileAt(base, path)
		if !has {
			continue
		}
		if local != baseContent {
			res.logf("  %s 远程已删除但本地有修改，保留", path)
			continue
		}
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			return err
		}
	}

	cfg, err := store.LoadConfig(root)
	if err != nil {
		return err
	}
	repo.Cfg = cfg
	if err := derive.Build(repo); err != nil {
		return err
	}
	return commitMerge(g, loc.Hash, rem.Hash)
}

func commitMerge(g *gitx.Git, parents ...plumbing.Hash) error {
	_, err := g.CommitMerge("errata: merge remote", parents)
	return err
}

func maxNumber(repo *store.Repo, rem *object.Commit) (int, error) {
	max, err := repo.NextNumber()
	if err != nil {
		return 0, err
	}
	max--
	ft, err := rem.Files()
	if err != nil {
		return 0, err
	}
	_ = ft.ForEach(func(f *object.File) error {
		if n, ok := problemNumber(f.Name); ok && n > max {
			max = n
		}
		return nil
	})
	return max, nil
}

// mergeConfig unions subjects and types by id; scalar fields keep the local value.
func mergeConfig(local, remote string) (string, error) {
	var l, r store.Config
	if err := yaml.Unmarshal([]byte(local), &l); err != nil {
		return "", err
	}
	if err := yaml.Unmarshal([]byte(remote), &r); err != nil {
		return "", err
	}
	l.Subjects = unionNamed(l.Subjects, r.Subjects)
	l.Types = unionNamed(l.Types, r.Types)
	b, err := yaml.Marshal(&l)
	return string(b), err
}

func unionNamed(local, remote []store.Named) []store.Named {
	have := map[string]int{}
	for i, n := range local {
		have[n.ID] = i
	}
	for _, n := range remote {
		i, ok := have[n.ID]
		if !ok {
			have[n.ID] = len(local)
			local = append(local, n)
			continue
		}
		seen := map[string]bool{}
		for _, a := range local[i].Aliases {
			seen[strings.ToLower(a)] = true
		}
		for _, a := range n.Aliases {
			if !seen[strings.ToLower(a)] {
				local[i].Aliases = append(local[i].Aliases, a)
			}
		}
	}
	return local
}
