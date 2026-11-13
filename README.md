# errata

基于 Git 的 AI-first 错题管理系统。题目是 Markdown 文件，分类是软链接，同步靠 Git。

```bash
go install github.com/chhongzh/errata/cmd/errata@latest
errata init mybook --remote git@github.com:you/mybook.git
cd mybook
errata serve        # 本地 Web 编辑界面
errata sync         # 提交、合并远程、推送
```

`errata init` 只应在空目录里运行，目录非空时会拒绝（`--force` 可跳过）。

## AI 使用

把本仓库的链接交给 agent，让它读取 [skills/errata/SKILL.md](skills/errata/SKILL.md) 即可。流程是：`errata schema` 取可用科目、题型和已有标签，再用 `errata add --json -` 一次入库。

## 仓库结构（errata init 生成）

```
problems/P<n>.md    所有题目，不分科目
staging/     草稿区（git 忽略）
assets/      题目里引用的图片
by-subject/ by-tag/ by-type/   指向 problems/ 的软链接，目录名为英文 id
.errata/     config.yaml 与 README.md.tmpl
docs/        GitHub Pages 站点（生成）
README.md    由 .errata/README.md.tmpl 生成
```

题目文件：YAML front matter（title、subject、type、tags、source、difficulty、created）加 `## 题干`、`## 答案`、`## 解析` 三节，公式用 LaTeX，只记正确内容。题号取自文件名。科目、题型、标签都用英文 kebab-case id，中文名只用于显示（配置在 `.errata/config.yaml`）。

## 命令

| 命令 | 作用 |
| --- | --- |
| `new` `edit` `commit` | 草稿流程：staging 到 problems |
| `add --json -` | 一步入库，含 `id` 则更新；整批校验，失败不写入 |
| `schema` | 科目、题型、已有标签、字段说明 |
| `list` `show` `tags` | 检索，`--json` 省 token |
| `set` `rm` | 改元数据、删除 |
| `subject add` `type add` | 新增科目、题型 |
| `serve [-p 7878]` | Web 编辑界面：增删改、LaTeX 实时预览、粘贴图片、同步。只绑定 127.0.0.1 |
| `fmt` | 规范化所有题目，重新生成派生文件 |
| `check [--clean]` | 检查格式、软链接、派生文件是否过期，可要求工作区干净 |
| `sync` | 提交、fetch、合并、推送 |
| `build` | 重新生成软链接、README、站点 |

go-git 实现，不依赖系统 git。HTTPS 用 `GITHUB_TOKEN`，SSH 用 ssh-agent。

## 合并策略

派生文件不参与合并，合并后重新生成。同一题两端都改时不丢数据：远程版本保留原编号，本地版本另存为新编号并标“（冲突副本）”。`config.yaml` 按 id 取并集。

## 发布

GitHub Pages 选择 `main` 分支的 `/docs` 目录。站点从 jsDelivr 加载 Bootstrap、jQuery、KaTeX。

站点数据按需加载，不会把整个题库塞进一个脚本：`index.json` 只含列表和筛选所需的元数据，`p/<ID>.json` 是单题的题干/答案/解析（打开题目时才取），`search.json` 是题干纯文本（第一次搜索时才取），图片在 `assets/`。需通过 HTTP 访问，不能直接双击 `index.html`。
