package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var LinkDirs = []string{"by-subject", "by-tag", "by-type"}

var dirSafe = strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-", "?", "-", "\"", "-", "<", "-", ">", "-", "|", "-")

func DirName(s string) string { return strings.TrimSpace(dirSafe.Replace(s)) }

// RebuildLinks recreates the by-* trees from scratch. Relative links keep the git diff empty when nothing changed.
func (r *Repo) RebuildLinks(ps []*Problem) error {
	for _, d := range LinkDirs {
		if err := os.RemoveAll(r.Path(d)); err != nil {
			return err
		}
	}
	link := func(dir, name string, p *Problem) error {
		name = DirName(name)
		if name == "" {
			return nil
		}
		folder := r.Path(dir, name)
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return err
		}
		target := filepath.Join("..", "..", ProblemsDir, p.ID+".md")
		if err := os.Symlink(target, filepath.Join(folder, p.ID+".md")); err != nil {
			return fmt.Errorf("创建软链接失败: %w（Windows 需开启开发者模式或管理员权限）", err)
		}
		return nil
	}
	for _, p := range ps {
		if err := link("by-subject", p.Meta.Subject, p); err != nil {
			return err
		}
		if err := link("by-type", p.Meta.Type, p); err != nil {
			return err
		}
		for _, t := range p.Meta.Tags {
			if err := link("by-tag", t, p); err != nil {
				return err
			}
		}
	}
	return nil
}
