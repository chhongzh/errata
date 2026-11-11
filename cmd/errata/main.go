package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/chhongzh/errata/internal/check"
	"github.com/chhongzh/errata/internal/derive"
	"github.com/chhongzh/errata/internal/gitx"
	"github.com/chhongzh/errata/internal/store"
	"github.com/chhongzh/errata/internal/syncer"
	"github.com/chhongzh/errata/internal/web"
)

type CLI struct {
	Dir string `short:"C" env:"ERRATA_REPO" type:"path" help:"错题仓库目录（默认从当前目录向上查找）。" default:"."`

	Init    InitCmd    `cmd:"" help:"初始化一个新的错题仓库。"`
	Clone   CloneCmd   `cmd:"" help:"克隆已有的远程错题仓库。"`
	New     NewCmd     `cmd:"" help:"在 staging/ 创建题目草稿（可用 --json 批量导入）。"`
	Edit    EditCmd    `cmd:"" help:"把已有题目取回 staging/ 以便修改（类似 git checkout）。"`
	Commit  CommitCmd  `cmd:"" help:"校验 staging/ 草稿并提交到 problems/。"`
	Add     AddCmd     `cmd:"" help:"一步完成 new + commit，从 JSON 新增或更新题目（AI 首选）。"`
	Set     SetCmd     `cmd:"" help:"修改已提交题目的元数据（科目/题型/标签等）。"`
	Rm      RmCmd      `cmd:"" help:"删除题目。"`
	List    ListCmd    `cmd:"" aliases:"ls" help:"列出/搜索题目。"`
	Show    ShowCmd    `cmd:"" help:"显示一道题。"`
	Tags    TagsCmd    `cmd:"" help:"列出所有标签及数量。"`
	Subject SubjectCmd `cmd:"" help:"管理科目。"`
	Type    TypeCmd    `cmd:"" help:"管理题型。"`
	Schema  SchemaCmd  `cmd:"" help:"输出 AI 需要的上下文：可用科目/题型/已有标签与 JSON 格式。"`
	Fmt     FmtCmd     `cmd:"" help:"规范化所有题目文件并重新生成软链接/README/站点。"`
	Check   CheckCmd   `cmd:"" help:"检查格式、软链接、派生文件，可选检查工作区是否干净。"`
	Build   BuildCmd   `cmd:"" help:"重新生成 by-*/ 软链接、README.md 和静态站点。"`
	Status  StatusCmd  `cmd:"" help:"显示草稿、未提交变更与远程同步状态。"`
	Remote  RemoteCmd  `cmd:"" help:"查看或设置远程仓库地址。"`
	Sync    SyncCmd    `cmd:"" help:"同步：提交本地变更、合并远程、推送。"`
	Serve   ServeCmd   `cmd:"" help:"启动本地 Web 编辑界面（开发模式，改动即写入仓库）。"`
}

func main() {
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("errata"),
		kong.Description("基于 Git 的 AI-first 错题管理系统。"),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{Compact: true}),
	)
	if err := ctx.Run(&cli); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func (c *CLI) repo() (*store.Repo, error) { return store.Open(c.Dir) }

func printf(format string, a ...any) { fmt.Printf(format, a...) }

func readJSON(src string) ([]store.Doc, error) {
	var data []byte
	var err error
	if src == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(src)
	}
	if err != nil {
		return nil, err
	}
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return nil, errors.New("JSON 输入为空")
	}
	var docs []store.Doc
	if data[0] == '[' {
		err = json.Unmarshal(data, &docs)
	} else {
		var d store.Doc
		err = json.Unmarshal(data, &d)
		docs = []store.Doc{d}
	}
	if err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}
	return docs, nil
}

func parseIDs(args []string) ([]int, error) {
	var out []int
	for _, a := range args {
		n, err := store.ParseID(a)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func rebuild(r *store.Repo) error { return derive.Build(r) }

func rel(r *store.Repo, p string) string {
	if s, err := filepath.Rel(r.Root, p); err == nil {
		return s
	}
	return p
}

type InitCmd struct {
	Path   string `arg:"" optional:"" default:"." help:"仓库目录。"`
	Title  string `help:"站点与 README 标题。" default:"Errata"`
	Remote string `help:"远程仓库 URL。"`
	Force  bool   `help:"目录非空时仍然初始化（不会自动提交已有文件）。"`
}

// strayEntries lists what already sits in dir, ignoring a bare .git and OS noise.
func strayEntries(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if n := e.Name(); n != ".git" && n != ".DS_Store" {
			out = append(out, n)
		}
	}
	return out
}

func (c *InitCmd) Run(cli *CLI) error {
	root, err := filepath.Abs(c.Path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, store.MetaDir, store.ConfigFile)); err == nil {
		return fmt.Errorf("%s 已经是 errata 仓库", root)
	}
	stray := strayEntries(root)
	if len(stray) > 0 && !c.Force {
		shown := stray
		if len(shown) > 5 {
			shown = append(shown[:5:5], fmt.Sprintf("等共 %d 项", len(stray)))
		}
		return fmt.Errorf("%s 不是空目录（%s）。errata 会在这里创建 problems/、by-*/、docs/、README.md 等，建议换一个空目录: errata init <新目录>；确需在此初始化请加 --force", root, strings.Join(shown, ", "))
	}
	for _, d := range []string{store.ProblemsDir, store.StagingDir, store.AssetsDir, store.MetaDir} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return err
		}
	}
	cfg := store.DefaultConfig(c.Title)
	if err := store.SaveConfig(root, cfg); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(syncer.GitIgnore), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, store.AssetsDir, ".gitkeep"), nil, 0o644); err != nil {
		return err
	}
	g, err := gitx.Open(root)
	if err != nil {
		if g, err = gitx.Init(root); err != nil {
			return err
		}
	}
	if c.Remote != "" {
		if err := g.SetRemote(c.Remote); err != nil {
			return err
		}
	}
	r := &store.Repo{Root: root, Cfg: cfg}
	if err := derive.EnsureTemplate(r); err != nil {
		return err
	}
	if err := derive.Build(r); err != nil {
		return err
	}
	if len(stray) > 0 {
		printf("已初始化 %s\n目录里原有 %d 项文件未被提交，请自行检查后再 errata sync。\n", root, len(stray))
		return nil
	}
	if _, err := g.CommitAll("errata: init"); err != nil {
		return err
	}
	printf("已初始化 %s\n下一步: errata new / errata add --json -；GitHub Pages 请选择 /%s 目录发布。\n", root, cfg.Site.Dir)
	return nil
}

type CloneCmd struct {
	URL  string `arg:"" help:"远程仓库 URL。"`
	Path string `arg:"" optional:"" help:"本地目录。"`
}

func (c *CloneCmd) Run(cli *CLI) error {
	dir := c.Path
	if dir == "" {
		dir = strings.TrimSuffix(filepath.Base(strings.TrimRight(c.URL, "/")), ".git")
	}
	g, err := gitx.Clone(c.URL, dir)
	if err != nil {
		return err
	}
	r, err := store.Open(g.Root)
	if err != nil {
		return fmt.Errorf("已克隆，但不是 errata 仓库: %w", err)
	}
	_ = os.MkdirAll(r.Path(store.StagingDir), 0o755)
	if err := derive.Build(r); err != nil {
		return err
	}
	printf("已克隆到 %s（%s）\n", dir, c.URL)
	return nil
}

type NewCmd struct {
	Title      string   `help:"标题。"`
	Subject    string   `short:"s" help:"科目（名称或别名）。"`
	Type       string   `short:"T" help:"题型。"`
	Tag        []string `short:"t" help:"标签，可重复或用逗号分隔。"`
	Source     string   `help:"来源，如“2024 期中卷 第 5 题”。"`
	Difficulty int      `short:"d" help:"难度 1-5。"`
	JSON       string   `help:"从 JSON 文件导入（- 表示 stdin），对象或数组。"`
}

func splitTags(in []string) []string {
	var out []string
	for _, t := range in {
		for _, x := range strings.FieldsFunc(t, func(r rune) bool { return r == ',' || r == '，' }) {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
	}
	return out
}

func (c *NewCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	var docs []store.Doc
	if c.JSON != "" {
		if docs, err = readJSON(c.JSON); err != nil {
			return err
		}
	} else {
		docs = []store.Doc{{Title: c.Title, Subject: c.Subject, Type: c.Type, Tags: splitTags(c.Tag), Source: c.Source, Difficulty: c.Difficulty}}
	}
	for _, d := range docs {
		n, err := r.NextNumber()
		if err != nil {
			return err
		}
		p := d.Problem()
		p.ID = store.FormatID(n)
		if s, ok := r.Cfg.Subject(p.Meta.Subject); ok {
			p.Meta.Subject = s.ID
		}
		if t, ok := r.Cfg.Type(p.Meta.Type); ok {
			p.Meta.Type = t.ID
		}
		if err := r.WriteStaged(p); err != nil {
			return err
		}
		printf("%s  %s\n", p.ID, rel(r, r.StagingPath(n)))
	}
	return nil
}

type EditCmd struct {
	ID   string `arg:"" help:"题号，如 P12。"`
	Open bool   `help:"用 $EDITOR 打开。"`
}

func (c *EditCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	n, err := store.ParseID(c.ID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(r.StagingPath(n)); err != nil {
		p, err := r.Load(n)
		if err != nil {
			return fmt.Errorf("题目 %s 不存在: %w", store.FormatID(n), err)
		}
		if err := r.WriteStaged(p); err != nil {
			return err
		}
	}
	path := r.StagingPath(n)
	printf("%s\n", rel(r, path))
	if c.Open {
		ed := os.Getenv("EDITOR")
		if ed == "" {
			return errors.New("未设置 $EDITOR")
		}
		cmd := exec.Command(ed, path)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	return nil
}

type CommitCmd struct {
	IDs []string `arg:"" optional:"" help:"要提交的题号；省略则提交 staging/ 中全部。"`
}

func (c *CommitCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	ids, err := parseIDs(c.IDs)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		if ids, err = r.Numbers(store.StagingDir); err != nil {
			return err
		}
	}
	if len(ids) == 0 {
		return errors.New("staging/ 为空")
	}
	var failed []string
	done := 0
	for _, n := range ids {
		p, err := r.LoadStaged(n)
		if err != nil {
			failed = append(failed, err.Error())
			continue
		}
		if err := r.Commit(p); err != nil {
			failed = append(failed, err.Error())
			continue
		}
		_ = os.Remove(r.StagingPath(n))
		printf("已提交 %s  %s\n", p.ID, p.Meta.Title)
		done++
	}
	if done > 0 {
		if err := rebuild(r); err != nil {
			return err
		}
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "\n"))
	}
	return nil
}

type AddCmd struct {
	JSON string `required:"" help:"JSON 文件（- 表示 stdin），对象或数组；含 id 则更新该题，否则新增。"`
}

func (c *AddCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	docs, err := readJSON(c.JSON)
	if err != nil {
		return err
	}
	// Validate everything first so a bad batch writes nothing.
	type job struct {
		p   *store.Problem
		upd bool
	}
	next, err := r.NextNumber()
	if err != nil {
		return err
	}
	var jobs []job
	var errs []string
	for i, d := range docs {
		p := d.Problem()
		upd := false
		if d.ID != "" {
			n, err := store.ParseID(d.ID)
			if err != nil {
				errs = append(errs, fmt.Sprintf("#%d: %v", i+1, err))
				continue
			}
			if _, err := os.Stat(r.ProblemPath(n)); err != nil {
				errs = append(errs, fmt.Sprintf("#%d: %s 不存在；新增请省略 id", i+1, store.FormatID(n)))
				continue
			}
			p.ID, upd = store.FormatID(n), true
			if old, err := r.Load(n); err == nil {
				p.Meta.Created = old.Meta.Created
			}
		} else {
			p.ID = store.FormatID(next)
			next++
		}
		if es := r.Normalize(p); len(es) > 0 {
			errs = append(errs, fmt.Sprintf("#%d (%s):\n  - %s", i+1, p.Meta.Title, strings.Join(es, "\n  - ")))
			continue
		}
		jobs = append(jobs, job{p, upd})
	}
	if len(errs) > 0 {
		return errors.New("校验失败，未写入任何题目:\n" + strings.Join(errs, "\n"))
	}
	for _, j := range jobs {
		if err := r.Commit(j.p); err != nil {
			return err
		}
		verb := "新增"
		if j.upd {
			verb = "更新"
		}
		_ = os.Remove(r.StagingPath(j.p.Number()))
		printf("%s %s  %s\n", verb, j.p.ID, j.p.Meta.Title)
	}
	return rebuild(r)
}

type SetCmd struct {
	ID         string   `arg:"" help:"题号。"`
	Title      *string  `help:"新标题。"`
	Subject    string   `short:"s" help:"新科目。"`
	Type       string   `short:"T" help:"新题型。"`
	AddTag     []string `short:"t" help:"添加标签。"`
	RmTag      []string `short:"x" help:"移除标签。"`
	Source     *string  `help:"新来源。"`
	Difficulty *int     `short:"d" help:"新难度。"`
}

func (c *SetCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	n, err := store.ParseID(c.ID)
	if err != nil {
		return err
	}
	p, err := r.Load(n)
	if err != nil {
		return err
	}
	m := &p.Meta
	if c.Title != nil {
		m.Title = *c.Title
	}
	if c.Subject != "" {
		m.Subject = c.Subject
	}
	if c.Type != "" {
		m.Type = c.Type
	}
	if c.Source != nil {
		m.Source = *c.Source
	}
	if c.Difficulty != nil {
		m.Difficulty = *c.Difficulty
	}
	rm := map[string]bool{}
	for _, t := range splitTags(c.RmTag) {
		rm[t] = true
	}
	var tags []string
	for _, t := range m.Tags {
		if !rm[t] {
			tags = append(tags, t)
		}
	}
	m.Tags = append(tags, splitTags(c.AddTag)...)
	if err := r.Commit(p); err != nil {
		return err
	}
	printf("已更新 %s\n", p.ID)
	return rebuild(r)
}

type RmCmd struct {
	IDs []string `arg:"" help:"题号。"`
}

func (c *RmCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	ids, err := parseIDs(c.IDs)
	if err != nil {
		return err
	}
	for _, n := range ids {
		ok, err := r.Remove(n)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s 不存在", store.FormatID(n))
		}
		printf("已删除 %s\n", store.FormatID(n))
	}
	return rebuild(r)
}

type ListCmd struct {
	Subject string   `short:"s" help:"按科目过滤。"`
	Type    string   `short:"T" help:"按题型过滤。"`
	Tag     []string `short:"t" help:"按标签过滤（多个为 AND）。"`
	Query   string   `short:"q" help:"在标题/题干/来源中搜索。"`
	JSON    bool     `help:"以 JSON 输出（不含正文）。"`
}

func (c *ListCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	ps, err := r.LoadAll()
	if err != nil {
		return err
	}
	subj, typ := c.Subject, c.Type
	if s, ok := r.Cfg.Subject(c.Subject); ok {
		subj = s.ID
	}
	if t, ok := r.Cfg.Type(c.Type); ok {
		typ = t.ID
	}
	var tags []string
	for _, t := range splitTags(c.Tag) {
		if n, ok := store.NormTag(t); ok {
			t = n
		}
		tags = append(tags, t)
	}
	q := strings.ToLower(c.Query)
	var out []*store.Problem
	for _, p := range ps {
		if subj != "" && p.Meta.Subject != subj || typ != "" && p.Meta.Type != typ {
			continue
		}
		have := map[string]bool{}
		for _, t := range p.Meta.Tags {
			have[t] = true
		}
		okTags := true
		for _, t := range tags {
			okTags = okTags && have[t]
		}
		if !okTags {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(p.Meta.Title+" "+p.Sections[store.SecStem]+" "+p.Meta.Source), q) {
			continue
		}
		out = append(out, p)
	}
	if c.JSON {
		docs := make([]store.Doc, len(out))
		for i, p := range out {
			d := p.Doc()
			d.Stem, d.Answer, d.Solution = "", "", ""
			docs[i] = d
		}
		return json.NewEncoder(os.Stdout).Encode(docs)
	}
	for _, p := range out {
		printf("%s\t%s\t%s\t%s\t[%s]\n", p.ID, p.Meta.Subject, p.Meta.Type, p.Meta.Title, strings.Join(p.Meta.Tags, ","))
	}
	return nil
}

type ShowCmd struct {
	ID     string `arg:"" help:"题号。"`
	JSON   bool   `help:"以 JSON 输出。"`
	Staged bool   `help:"显示 staging/ 中的草稿。"`
}

func (c *ShowCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	n, err := store.ParseID(c.ID)
	if err != nil {
		return err
	}
	path, load := r.ProblemPath(n), r.Load
	if c.Staged {
		path, load = r.StagingPath(n), r.LoadStaged
	}
	if c.JSON {
		p, err := load(n)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(p.Doc())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(b)
	return err
}

type TagsCmd struct {
	JSON bool `help:"以 JSON 输出。"`
}

func (c *TagsCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	ps, err := r.LoadAll()
	if err != nil {
		return err
	}
	tags := store.Tags(ps)
	if c.JSON {
		return json.NewEncoder(os.Stdout).Encode(tags)
	}
	keys := store.SortedKeys(tags)
	sort.SliceStable(keys, func(i, j int) bool { return tags[keys[i]] > tags[keys[j]] })
	for _, k := range keys {
		printf("%4d  %s\n", tags[k], k)
	}
	return nil
}

type SubjectCmd struct {
	Add  SubjectAdd  `cmd:"" help:"新增科目。"`
	List SubjectList `cmd:"" aliases:"ls" help:"列出科目。"`
}
type SubjectAdd struct {
	ID      string   `arg:"" help:"英文 id（小写 kebab-case，用作目录名），如 math。"`
	Name    string   `required:"" help:"显示名称，如 数学。"`
	Aliases []string `short:"a" help:"别名，可重复。"`
}
type SubjectList struct{}

func (c *SubjectAdd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	if err := addNamed(&r.Cfg.Subjects, c.ID, c.Name, c.Aliases); err != nil {
		return err
	}
	if err := store.SaveConfig(r.Root, r.Cfg); err != nil {
		return err
	}
	printf("已添加科目 %s (%s)\n", c.ID, c.Name)
	return nil
}
func (c *SubjectList) Run(cli *CLI) error {
	return listNamed(cli, func(r *store.Repo) []store.Named { return r.Cfg.Subjects })
}

type TypeCmd struct {
	Add  TypeAdd  `cmd:"" help:"新增题型。"`
	List TypeList `cmd:"" aliases:"ls" help:"列出题型。"`
}
type TypeAdd struct {
	ID      string   `arg:"" help:"英文 id（小写 kebab-case），如 free-response。"`
	Name    string   `required:"" help:"显示名称，如 解答题。"`
	Aliases []string `short:"a" help:"别名，可重复。"`
}
type TypeList struct{}

func (c *TypeAdd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	if err := addNamed(&r.Cfg.Types, c.ID, c.Name, c.Aliases); err != nil {
		return err
	}
	if err := store.SaveConfig(r.Root, r.Cfg); err != nil {
		return err
	}
	printf("已添加题型 %s (%s)\n", c.ID, c.Name)
	return nil
}
func (c *TypeList) Run(cli *CLI) error {
	return listNamed(cli, func(r *store.Repo) []store.Named { return r.Cfg.Types })
}

func addNamed(list *[]store.Named, id, name string, aliases []string) error {
	if !store.ValidSlug(id) {
		return fmt.Errorf("id %q 必须是小写英文 kebab-case（如 free-response）", id)
	}
	if store.HasNamed(*list, id) {
		return fmt.Errorf("%q 已存在", id)
	}
	*list = append(*list, store.Named{ID: id, Name: name, Aliases: splitTags(aliases)})
	return nil
}

func listNamed(cli *CLI, get func(*store.Repo) []store.Named) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	for _, n := range get(r) {
		printf("%s\t%s\t%s\n", n.ID, n.Name, strings.Join(n.Aliases, ","))
	}
	return nil
}

type SchemaCmd struct{}

func (c *SchemaCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	ps, err := r.LoadAll()
	if err != nil {
		return err
	}
	tags := store.Tags(ps)
	keys := store.SortedKeys(tags)
	sort.SliceStable(keys, func(i, j int) bool { return tags[keys[i]] > tags[keys[j]] })
	out := map[string]any{
		"subjects":   r.Cfg.Subjects,
		"types":      r.Cfg.Types,
		"known_tags": keys,
		"problems":   len(ps),
		"next_id":    nextID(r),
		"add":        "errata add --json -   # stdin 传入对象或数组；含 id 则更新该题",
		"fields": map[string]string{
			"title":      "必填。一句话概括考点，如 \"二次函数在闭区间上的最值（含参）\"",
			"subject":    "必填。subjects 中的 id（或 name、别名）",
			"type":       "必填。types 中的 id（或 name、别名）；缺少时先 errata type add",
			"tags":       "必填，至少 1 个。英文 kebab-case（quadratic-function）；优先复用 known_tags，不要写科目或题型",
			"source":     "可选。试卷/页码/题号",
			"difficulty": "可选。1-5",
			"stem":       "必填。题干，Markdown + LaTeX（$..$ / $$..$$）；选择题把选项写进题干",
			"answer":     "已知时必填。只写正确答案",
			"solution":   "可选。正确的解题步骤，Markdown + LaTeX",
			"id":         "仅更新已有题时填写，如 P12",
		},
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func nextID(r *store.Repo) string {
	n, _ := r.NextNumber()
	return store.FormatID(n)
}

type FmtCmd struct{}

func (c *FmtCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	changed, problems, err := check.Format(r)
	if err != nil {
		return err
	}
	for _, p := range changed {
		printf("已格式化 %s\n", p)
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d 个文件无法格式化，派生文件未重新生成", len(problems))
	}
	printf("完成（%d 个文件被改写，软链接/README/站点已重新生成）\n", len(changed))
	return nil
}

type CheckCmd struct {
	Clean bool `help:"同时要求 Git 工作区干净。"`
	JSON  bool `help:"以 JSON 输出。"`
}

func (c *CheckCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	is, err := check.Run(r, check.Options{Clean: c.Clean})
	if err != nil {
		return err
	}
	if c.JSON {
		if is == nil {
			is = []check.Issue{}
		}
		_ = json.NewEncoder(os.Stdout).Encode(is)
	} else {
		for _, i := range is {
			fmt.Println(i)
		}
	}
	if check.HasErrors(is) {
		return errors.New("检查未通过")
	}
	if !c.JSON {
		printf("检查通过\n")
	}
	return nil
}

type BuildCmd struct{}

func (c *BuildCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	if err := rebuild(r); err != nil {
		return err
	}
	printf("已生成 by-subject/ by-tag/ by-type/ README.md %s/\n", r.Cfg.Site.Dir)
	return nil
}

type StatusCmd struct{}

func (c *StatusCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	if ns, _ := r.Numbers(store.StagingDir); len(ns) > 0 {
		printf("暂存区草稿:\n")
		for _, n := range ns {
			title := ""
			if p, err := r.LoadStaged(n); err == nil {
				title = p.Meta.Title
			}
			printf("  %s  %s\n", store.FormatID(n), title)
		}
	} else {
		printf("暂存区为空\n")
	}
	g, err := gitx.Open(r.Root)
	if err != nil {
		return nil
	}
	dirty, _ := g.Dirty()
	if len(dirty) == 0 {
		printf("工作区干净\n")
	} else {
		printf("未提交的变更 (%d):\n", len(dirty))
		for _, d := range dirty {
			printf("  %s\n", d)
		}
	}
	if url := g.RemoteURL(); url != "" {
		printf("远程: %s (%s)\n", url, g.Branch())
	} else {
		printf("远程: 未配置\n")
	}
	return nil
}

type RemoteCmd struct {
	URL string `arg:"" optional:"" help:"远程 URL；省略则显示当前值。"`
}

func (c *RemoteCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	g, err := gitx.Open(r.Root)
	if err != nil {
		return err
	}
	if c.URL == "" {
		printf("%s\n", g.RemoteURL())
		return nil
	}
	return g.SetRemote(c.URL)
}

type SyncCmd struct {
	Message string `short:"m" help:"提交信息（默认自动生成）。"`
	NoPush  bool   `help:"只合并，不推送。"`
	Offline bool   `help:"只做本地提交，不联网。"`
}

func (c *SyncCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	res, err := syncer.Sync(r, syncer.Options{Message: c.Message, NoPush: c.NoPush, Offline: c.Offline})
	if res != nil {
		for _, l := range res.Log {
			printf("%s\n", l)
		}
	}
	return err
}

type ServeCmd struct {
	Port int  `short:"p" default:"7878" help:"监听端口（仅绑定 127.0.0.1）。"`
	Open bool `help:"启动后用默认浏览器打开。"`
}

func (c *ServeCmd) Run(cli *CLI) error {
	r, err := cli.repo()
	if err != nil {
		return err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", c.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("无法监听 %s（换个端口: errata serve -p 8080）: %w", addr, err)
	}
	url := "http://" + addr
	printf("编辑界面: %s\n仓库: %s\nCtrl-C 退出。改动会直接写入仓库，用 errata sync 或界面里的“同步”提交推送。\n", url, r.Root)
	if c.Open {
		openBrowser(url)
	}
	return http.Serve(ln, web.New(r.Root).Handler())
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
