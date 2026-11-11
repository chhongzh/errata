package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	MetaDir    = ".errata"
	ConfigFile = "config.yaml"
	ReadmeTmpl = "README.md.tmpl"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Named is a subject or problem type: a kebab-case id used in files and
// directory names, plus a display name for people.
type Named struct {
	ID      string   `yaml:"id" json:"id"`
	Name    string   `yaml:"name" json:"name"`
	Aliases []string `yaml:"aliases,omitempty" json:"aliases,omitempty"`
}

type SiteConfig struct {
	Dir string `yaml:"dir"`
}

type Config struct {
	Title    string     `yaml:"title"`
	Subjects []Named    `yaml:"subjects"`
	Types    []Named    `yaml:"types"`
	Site     SiteConfig `yaml:"site"`
}

func DefaultConfig(title string) *Config {
	if title == "" {
		title = "Errata"
	}
	return &Config{
		Title: title,
		Subjects: []Named{
			{ID: "chinese", Name: "语文"}, {ID: "math", Name: "数学"}, {ID: "english", Name: "英语"},
			{ID: "physics", Name: "物理"}, {ID: "chemistry", Name: "化学"}, {ID: "biology", Name: "生物"},
			{ID: "history", Name: "历史"}, {ID: "geography", Name: "地理"}, {ID: "politics", Name: "政治"},
		},
		Types: []Named{
			{ID: "choice", Name: "选择题"}, {ID: "fill-blank", Name: "填空题"},
			{ID: "free-response", Name: "解答题"}, {ID: "calculation", Name: "计算题"},
			{ID: "experiment", Name: "实验题"}, {ID: "grammar-fill", Name: "语法填空"},
			{ID: "poetry-recall", Name: "古诗词默写"}, {ID: "reading", Name: "阅读题"},
			{ID: "composite", Name: "综合大题"},
		},
		Site: SiteConfig{Dir: "docs"},
	}
}

func ValidSlug(s string) bool { return slugRe.MatchString(s) }

// NormTag lowercases a tag and turns spaces and underscores into hyphens, so
// tag directory names stay portable English kebab-case.
func NormTag(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(" ", "-", "_", "-").Replace(s)
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	return s, ValidSlug(s)
}

func find(list []Named, s string) (Named, bool) {
	s = strings.TrimSpace(s)
	for _, n := range list {
		if strings.EqualFold(n.ID, s) || n.Name == s {
			return n, true
		}
		for _, a := range n.Aliases {
			if strings.EqualFold(a, s) {
				return n, true
			}
		}
	}
	return Named{}, false
}

func (c *Config) Subject(s string) (Named, bool) { return find(c.Subjects, s) }
func (c *Config) Type(s string) (Named, bool)    { return find(c.Types, s) }

func label(list []Named, id string) string {
	for _, n := range list {
		if n.ID == id {
			return n.Name
		}
	}
	return id
}

func (c *Config) SubjectLabel(id string) string { return label(c.Subjects, id) }
func (c *Config) TypeLabel(id string) string    { return label(c.Types, id) }

// Describe renders "math (数学), physics (物理)" for error messages.
func Describe(list []Named) string {
	parts := make([]string, len(list))
	for i, n := range list {
		parts[i] = n.ID + " (" + n.Name + ")"
	}
	return strings.Join(parts, ", ")
}

func (c *Config) validate() error {
	for kind, list := range map[string][]Named{"subjects": c.Subjects, "types": c.Types} {
		seen := map[string]bool{}
		for _, n := range list {
			if !ValidSlug(n.ID) || n.Name == "" {
				return fmt.Errorf("config.yaml: %s 条目 %+v 缺少合法的 id（小写英文 kebab-case）或 name", kind, n)
			}
			if seen[n.ID] {
				return fmt.Errorf("config.yaml: %s 中 id %q 重复", kind, n.ID)
			}
			seen[n.ID] = true
		}
	}
	return nil
}

func LoadConfig(root string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(root, MetaDir, ConfigFile))
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, err
	}
	if c.Site.Dir == "" {
		c.Site.Dir = "docs"
	}
	return c, c.validate()
}

func SaveConfig(root string, c *Config) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	p := filepath.Join(root, MetaDir, ConfigFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func Find(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, MetaDir, ConfigFile)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("未找到 errata 仓库（缺少 .errata/config.yaml）：请在仓库内运行，或用 -C <dir> / 环境变量 ERRATA_REPO 指定，或先 `errata init`")
		}
		dir = parent
	}
}

// HasNamed reports whether s matches an id, name or alias in list.
func HasNamed(list []Named, s string) bool {
	_, ok := find(list, s)
	return ok
}
