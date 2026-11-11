package gitx

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

const Remote = "origin"

type Git struct {
	Root string
	R    *git.Repository
}

func Init(root string) (*Git, error) {
	r, err := git.PlainInitWithOptions(root, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")},
	})
	if err != nil {
		return nil, err
	}
	return &Git{root, r}, nil
}

func Open(root string) (*Git, error) {
	r, err := git.PlainOpen(root)
	if err != nil {
		return nil, err
	}
	return &Git{root, r}, nil
}

func Clone(url, dir string) (*Git, error) {
	r, err := git.PlainClone(dir, false, &git.CloneOptions{URL: url, Auth: authFor(url), Progress: nil})
	if err != nil {
		return nil, err
	}
	return &Git{dir, r}, nil
}

// authFor: SSH remotes use the agent, HTTPS remotes use ERRATA_GIT_TOKEN,
// GITHUB_TOKEN or GH_TOKEN.
func authFor(url string) transport.AuthMethod {
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		for _, k := range []string{"ERRATA_GIT_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
			if t := os.Getenv(k); t != "" {
				user := os.Getenv("ERRATA_GIT_USER")
				if user == "" {
					user = "errata"
				}
				return &http.BasicAuth{Username: user, Password: t}
			}
		}
		return nil
	}
	if a, err := gitssh.NewSSHAgentAuth(gitssh.DefaultUsername); err == nil {
		return a
	}
	return nil
}

func (g *Git) RemoteURL() string {
	rm, err := g.R.Remote(Remote)
	if err != nil || len(rm.Config().URLs) == 0 {
		return ""
	}
	return rm.Config().URLs[0]
}

func (g *Git) SetRemote(url string) error {
	_ = g.R.DeleteRemote(Remote)
	_, err := g.R.CreateRemote(&config.RemoteConfig{Name: Remote, URLs: []string{url}})
	return err
}

func (g *Git) Branch() string {
	h, err := g.R.Head()
	if err != nil || !h.Name().IsBranch() {
		return "main"
	}
	return h.Name().Short()
}

func (g *Git) hasHead() bool {
	_, err := g.R.Head()
	return err == nil
}

func (g *Git) Dirty() ([]string, error) {
	wt, err := g.R.Worktree()
	if err != nil {
		return nil, err
	}
	st, err := wt.Status()
	if err != nil {
		return nil, err
	}
	var out []string
	for p, s := range st {
		out = append(out, fmt.Sprintf("%c%c %s", s.Staging, s.Worktree, p))
	}
	sort.Strings(out)
	return out, nil
}

func signature() *object.Signature {
	name, email := os.Getenv("GIT_AUTHOR_NAME"), os.Getenv("GIT_AUTHOR_EMAIL")
	if name == "" || email == "" {
		if cfg, err := config.LoadConfig(config.GlobalScope); err == nil {
			if name == "" {
				name = cfg.User.Name
			}
			if email == "" {
				email = cfg.User.Email
			}
		}
	}
	if name == "" {
		name = "errata"
	}
	if email == "" {
		email = "errata@localhost"
	}
	return &object.Signature{Name: name, Email: email, When: time.Now()}
}

// CommitAll reports false when there was nothing to commit.
func (g *Git) CommitAll(msg string) (bool, error) {
	wt, err := g.R.Worktree()
	if err != nil {
		return false, err
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return false, err
	}
	st, err := wt.Status()
	if err != nil {
		return false, err
	}
	if st.IsClean() {
		return false, nil
	}
	_, err = wt.Commit(msg, &git.CommitOptions{Author: signature()})
	return err == nil, err
}

func (g *Git) Fetch() error {
	url := g.RemoteURL()
	if url == "" {
		return errNoRemote
	}
	err := g.R.Fetch(&git.FetchOptions{
		RemoteName: Remote, Auth: authFor(url), Force: true,
		RefSpecs: []config.RefSpec{"+refs/heads/*:refs/remotes/origin/*"},
	})
	if errors.Is(err, git.NoErrAlreadyUpToDate) || errors.Is(err, transport.ErrEmptyRemoteRepository) {
		return nil
	}
	return err
}

func (g *Git) Push() error {
	url := g.RemoteURL()
	if url == "" {
		return errNoRemote
	}
	b := g.Branch()
	err := g.R.Push(&git.PushOptions{
		RemoteName: Remote, Auth: authFor(url),
		RefSpecs: []config.RefSpec{config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", b, b))},
	})
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil
	}
	return err
}

var errNoRemote = errors.New("未配置远程仓库，请先运行: errata remote <url>")

func ErrNoRemote() error { return errNoRemote }

func (g *Git) IsNoRemote(err error) bool { return errors.Is(err, errNoRemote) }

func (g *Git) localHead() (*object.Commit, error) {
	h, err := g.R.Head()
	if err != nil {
		return nil, err
	}
	return g.R.CommitObject(h.Hash())
}

func (g *Git) remoteHead() (*object.Commit, error) {
	ref, err := g.R.Reference(plumbing.NewRemoteReferenceName(Remote, g.Branch()), true)
	if err != nil {
		return nil, nil // remote has no such branch yet
	}
	return g.R.CommitObject(ref.Hash())
}

type Divergence int

const (
	InSync Divergence = iota
	LocalAhead
	RemoteAhead
	Diverged
	NoRemoteBranch
	NoLocalCommits
)

func (g *Git) Compare() (Divergence, *object.Commit, *object.Commit, error) {
	rem, err := g.remoteHead()
	if err != nil {
		return 0, nil, nil, err
	}
	if !g.hasHead() {
		if rem == nil {
			return InSync, nil, nil, nil
		}
		return NoLocalCommits, nil, rem, nil
	}
	loc, err := g.localHead()
	if err != nil {
		return 0, nil, nil, err
	}
	if rem == nil {
		return NoRemoteBranch, loc, nil, nil
	}
	if loc.Hash == rem.Hash {
		return InSync, loc, rem, nil
	}
	if ok, err := rem.IsAncestor(loc); err != nil {
		return 0, nil, nil, err
	} else if ok {
		return LocalAhead, loc, rem, nil
	}
	if ok, err := loc.IsAncestor(rem); err != nil {
		return 0, nil, nil, err
	} else if ok {
		return RemoteAhead, loc, rem, nil
	}
	return Diverged, loc, rem, nil
}

func (g *Git) FastForward(to *object.Commit) error {
	return g.ResetHard(to.Hash)
}

func (g *Git) ResetHard(h plumbing.Hash) error {
	wt, err := g.R.Worktree()
	if err != nil {
		return err
	}
	if !g.hasHead() {
		ref := plumbing.NewHashReference(plumbing.NewBranchReferenceName(g.Branch()), h)
		if err := g.R.Storer.SetReference(ref); err != nil {
			return err
		}
		head := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName(g.Branch()))
		if err := g.R.Storer.SetReference(head); err != nil {
			return err
		}
	}
	return wt.Reset(&git.ResetOptions{Commit: h, Mode: git.HardReset})
}

type Changes struct {
	Added    map[string]string // path -> content
	Modified map[string]string
	Deleted  []string
}

// Diff compares file trees of from -> to, restricted by keep.
func Diff(from, to *object.Commit, keep func(path string) bool) (*Changes, error) {
	ft, err := from.Tree()
	if err != nil {
		return nil, err
	}
	tt, err := to.Tree()
	if err != nil {
		return nil, err
	}
	changes, err := ft.Diff(tt)
	if err != nil {
		return nil, err
	}
	c := &Changes{Added: map[string]string{}, Modified: map[string]string{}}
	for _, ch := range changes {
		act, err := ch.Action()
		if err != nil {
			return nil, err
		}
		_, ff, err := ch.Files()
		if err != nil {
			return nil, err
		}
		switch {
		case ff == nil: // deleted
			if keep(ch.From.Name) {
				c.Deleted = append(c.Deleted, ch.From.Name)
			}
		default:
			if !keep(ch.To.Name) {
				continue
			}
			content, err := ff.Contents()
			if err != nil {
				return nil, err
			}
			if act.String() == "Insert" {
				c.Added[ch.To.Name] = content
			} else {
				c.Modified[ch.To.Name] = content
			}
		}
	}
	sort.Strings(c.Deleted)
	return c, nil
}

func FileAt(c *object.Commit, path string) (string, bool) {
	f, err := c.File(path)
	if err != nil {
		return "", false
	}
	s, err := f.Contents()
	return s, err == nil
}

func MergeBase(a, b *object.Commit) (*object.Commit, error) {
	bases, err := a.MergeBase(b)
	if err != nil {
		return nil, err
	}
	if len(bases) == 0 {
		return nil, errors.New("本地与远程历史无共同祖先（不相关的仓库）")
	}
	return bases[0], nil
}

func (g *Git) CommitMerge(msg string, parents []plumbing.Hash) (plumbing.Hash, error) {
	wt, err := g.R.Worktree()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return plumbing.ZeroHash, err
	}
	return wt.Commit(msg, &git.CommitOptions{Author: signature(), Parents: parents, AllowEmptyCommits: true})
}
