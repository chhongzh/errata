---
name: errata
description: 管理基于 Git 的错题本（errata CLI）。当用户发来错题或题目图片（题干照片、答案照片、手写解答）、要求录入、整理、检索错题、按标签或科目查题、同步错题仓库、检查或修复错题库格式时使用。
---

# errata：错题本

错题库是一个 Git 仓库，全部用文件表示，由 `errata` CLI 操作。`by-*/`、`README.md`、`docs/` 是生成的派生文件，不要手改；也不要直接用 `git` 改库，一律走 CLI。

## 仓库结构

```
problems/P<n>.md       所有题目，不分科目，按编号
staging/        草稿（不入库）
assets/  题目引用的图片
by-subject/ by-tag/ by-type/  指向 problems/ 的软链接，目录名都是英文 id
.errata/        config.yaml（科目、题型）与 README 模板
docs/    GitHub Pages 静态站点
```

在仓库内运行即可；否则加 `-C <dir>` 或设置 `ERRATA_REPO`。找不到仓库时问用户，不要擅自 `init`。

## 录入工作流（图片到错题）

用户会给题干照片，以及答案照片（有的话）或手写解答照片（没有标准答案时）。

1. 运行 `errata schema`，得到可用 `subjects`、`types`（各有英文 `id` 和中文 `name`）、`known_tags` 和字段说明。录入只需要读它，不要去 `ls` 或 `cat` 整个仓库。
2. 识别图片，生成 JSON，通过 stdin 一次提交（可批量，用数组）：

```bash
errata add --json - <<'EOF'
{
  "title": "二次函数在闭区间上的最值（含参）",
  "subject": "math",
  "type": "free-response",
  "tags": ["quadratic-function", "case-analysis", "extremum"],
  "source": "高一期中 第 18 题",
  "difficulty": 3,
  "stem": "已知函数 $f(x)=x^2-2ax+3$ 在区间 $[0,2]$ 上的最小值为 $g(a)$，求 $g(a)$。",
  "answer": "$g(a)=\\begin{cases}3 & a\\le 0\\\\ 3-a^2 & 0<a<2\\\\ 7-4a & a\\ge 2\\end{cases}$",
  "solution": "对称轴为 $x=a$，按 $a$ 与区间 $[0,2]$ 的位置分三类：\n\n1. $a\\le 0$：...\n2. $0<a<2$：...\n3. $a\\ge 2$：..."
}
EOF
```

3. 校验失败会整批不写入并说明原因，按提示修正后重试。用户要求同步或推送时再运行 `errata sync`。

## 内容规范

- 只记正确内容：不记录错误答案、错因、笔记栏、总结栏。三个字段：`stem` 题干、`answer` 正确答案、`solution` 正确的解题步骤。
- 公式一律 LaTeX：行内 `$...$`，独立公式 `$$...$$`。写进 JSON 时反斜杠要转义为 `\\`。
- 有答案照片：以答案照片为准写 `answer`，再整理 `solution`。
- 只有手写解答：先判断手写过程是否正确，`answer` 和 `solution` 写正确的结果与步骤，手写有错的地方不要照抄。拿不准的答案宁可留空，不要编造。
- 选择题：选项写进 `stem`（每项一行 `- A. ...`），`answer` 写字母。
- 整道大题可以整体收录：小题用 `（1）（2）` 或列表，章节标题只能用 `###` 及以下。
- 图形、图表无法转成文字时：用文字描述关键信息。需要保留原图，就让用户把图片放进 `assets/`，在题干里用 `![](../assets/xxx.png)` 引用。
- 积累意义小的题型（英语阅读理解、连线匹配）不要主动录入，先问用户。
- 古诗词默写：`type` 用 `poetry-recall`，`stem` 写上下文与空缺，`answer` 写完整原句。
- `subject`、`type` 用 `schema` 返回的英文 `id`（中文名和别名也能识别）。
- `tags` 必须是英文 kebab-case，知识点粒度（`quadratic-function`、`lorentz-force`），2 到 5 个。先看 `known_tags`，能复用就不要新造近义词。不要把科目或题型写成标签。
- 科目或题型不在列表里：先问用户，同意后再 `errata subject add <id> --name <中文名>` 或 `errata type add <id> --name <中文名>`。

## 其他命令

```bash
errata list -s math -t quadratic-function -q 最值 [--json]   # 过滤、搜索（--json 不含正文，省 token）
errata show P12 [--json]       # 查看一题
errata tags [--json]    # 标签统计
errata set P12 -t new-tag -x old-tag -s physics   # 改元数据
errata edit P12  # 取回 staging/P12.md 改正文，再 errata commit P12
errata rm P12
errata add --json -     # JSON 含 "id":"P12" 即更新该题（先 show --json 取原内容再改）
errata serve     # 本地 Web 编辑界面，给人用
```

草稿流程（人类习惯）：`errata new -s math -T free-response -t derivative --title ...`，编辑 `staging/P<n>.md`，再 `errata commit`。AI 默认用 `add --json`，不需要草稿。

## 质量与同步

```bash
errata fmt     # 规范化所有题目，并重新生成软链接、README、站点
errata check          # 检查格式、软链接（缺失、多余、指错、悬空）、派生文件是否过期
errata check --clean  # 额外要求 Git 工作区干净
errata sync [-m msg]  # 提交、拉取、合并、推送
```

- `check` 报错时先 `errata fmt`，仍报错就按提示改源文件。
- `sync` 遇到两端同时修改同一题，会保留远程版本的原编号，把本地版本另存为新编号，标题加“（冲突副本）”。同步后告诉用户去合并。
- 凭据：HTTPS 用环境变量 `GITHUB_TOKEN`，SSH 用 ssh-agent。没有权限时如实告诉用户，不要绕过。

## 省 token 的做法

- 录入只需要 `schema` 加一次 `add`，不要读 `problems/` 或 README。
- 检索用 `list --json` 和 `tags --json`，只对命中的题 `show`。
- 一次拍了多张图，合并成一个 JSON 数组，一次 `add`。
