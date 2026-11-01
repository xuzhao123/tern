# 内置的斜杠命令与 skill

状态：已实现（2026-10-24，tern 实现；第 5 节验收全部通过，第 7–10 条由 Claude Code 用真实模型跑）。review 时补了第 3 节 README 一行（`-hooks` 那行的三层、README 不写“这一期”）。

## 1. 为什么要改

数据目录改成 `~/.tern` 以后（tern-home-default.md），仓库里的 `commands/review.md`、`skills/code-review/` 就不生效了：它们只在 `TERN_HOME` 是源码根目录时才会被找到。现在想用，得手动拷到 `~/.tern`。2026-10-24 用户看到这一点后说“应该有一些内置的 command 和内置的 skills”。

受影响的还有评测：`eval/cases.jsonl` 的 `skill-review` 这题带 `-skills`，要用 `code-review`。`TERN_HOME` 是 `~/.tern` 时找不到这个 skill，这题就测不出 skill 了。

Claude Code、Codex 都自带一些命令（`/init`、`/review`），装好就能用，个人和项目里同名的文件可以覆盖。tern 照这个来。

## 2. 改什么

**内置的随程序发布**：内置的命令和 skill 用 `//go:embed` 编进程序，不往磁盘上写，也不需要安装。改内置的就是改源码、重新编译。

- 内置命令放 `internal/commands/builtin/<名字>.md`，由 `internal/commands` 嵌入。
- 内置 skill 放 `internal/skills/builtin/<名字>/SKILL.md`（`references/` 也可以有），由 `internal/skills` 嵌入。
- 这一期内置三份：现在仓库里的 `commands/review.md` 和 `skills/code-review/` 原样搬过去（`git mv`），仓库根目录的 `commands/`、`skills/` 删掉，以后只在 `~/.tern` 里放个人的；再新加一个 `/init`（第 2.1 节）。

### 2.1 `/init`

照 Codex、Claude Code 的 `/init`：看一遍工作区，生成或更新根目录的 `AGENTS.md`。tern 每轮都把它放进系统提示词（`internal/workspace/context.go`），写得好，以后在这个仓库里的每一轮都受益。`internal/commands/builtin/init.md` 的全文：

```markdown
---
description: 看一遍工作区，生成或更新根目录的 AGENTS.md（项目是什么、构建与测试命令、约定）
argument-hint: [补充要求，可选]
---
为这个工作区生成或更新根目录的 AGENTS.md。它会在以后每一轮放进系统提示词，读者是以后在这个仓库里干活的 agent。

1. 先看根目录有没有 AGENTS.md 或 CLAUDE.md。有就在原文上改：保留维护者写的约定，只补缺的、改过时的，不整份重写。
2. 弄清这个仓库：用 glob、grep、read_file 看目录结构、README、构建配置（go.mod、package.json、Makefile、pyproject.toml、Cargo.toml 等）和 CI 配置；有 bash 时可以跑 `git -C /workspace log --oneline -15` 看最近在改什么。看到能写清楚就停，不逐个读源码文件。
3. 只写从文件里能确认的，每条尽量短：
   - 项目是什么，主要目录各放什么；
   - 构建、测试、格式化、lint 的命令，照构建配置和 CI 里实际写的抄；
   - 不显然的约定：命名、错误处理、提交方式、生成的文件不要手改、需要的环境变量等，现有代码或文档里能看出来的才写。
4. 不写泛泛的建议（“保持代码整洁”“记得写测试”），不编仓库里没有的命令。拿不准的列在最后的“待确认”里，留给用户。
5. 用 write_file（新建）或 edit_file（已有）写到工作区根目录的 AGENTS.md。最后用几句话说明写了哪些、改了哪些、哪些待确认。

补充要求：$ARGUMENTS
```

没有选工作区的对话里，`/init` 照样展开，模型会发现没有工作区可写；不另做检查。

### 2.2 三层，同名时后面的覆盖前面的

| | 命令 | skill |
| --- | --- | --- |
| 内置 | `internal/commands/builtin/` | `internal/skills/builtin/` |
| 个人 | `$TERN_HOME/commands/` | `$TERN_HOME/skills/` |
| 项目 | `<工作区>/.agents/commands/` | 没有（这一期不做） |

覆盖是整份替换：个人的 `skills/code-review/SKILL.md` 一旦存在，内置的 `code-review` 就不出现了，列表里也只有个人那一份。

### 2.3 开关与删除

- skill 照旧在 Skills 中心按名字开关（`.data/hub.json` 的 `disabled_skills`），对内置的同样有效。
- 内置的不能删：Skills 中心不显示“删除”，接口返回 400。
- 命令没有开关；不想要内置的，就在个人或项目里写一份同名的覆盖它。

### 2.4 chat 里也能用斜杠命令

现在 chat 把所有 `/` 开头的输入都当成 chat 自己的命令，`/review` 会报“不认识的命令”，只有网页端和 `-question` 能用斜杠命令。改成和网页端一样：
- chat 自己的命令（`/help`、`/rewind`……）优先。
- 剩下的、形如 `/名字 参数` 的输入，当成一轮发出去，由 agent 展开。没有这个命令时，agent 照旧报“没有 /x 这个命令；可用的命令：…”，和网页端一样。
- `/help` 末尾列出可用的斜杠命令（`command/list`，带上 `-workspace` 的值；没给就只有内置和个人的），每条一行：`/名字 参数提示  说明（内置/个人/项目）`。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/commands/builtin/review.md` | 从 `commands/review.md` 搬过来，内容不改 |
| `internal/commands/builtin/init.md` | 新建，全文见第 2.1 节 |
| `internal/skills/builtin/code-review/SKILL.md` | 从 `skills/code-review/SKILL.md` 搬过来，内容不改 |
| `internal/commands/commands.go` | `//go:embed builtin`。`List` 先放内置的（`Scope` 是 `builtin`），再放个人级、项目级，同名时后放的覆盖前面的（现在的 map 写法照用）。内置命令的正文从嵌入的文件读，`read` 和 `Expand` 对内置的走嵌入文件，对磁盘上的读法不变。导出 `IsInvocation(input string) bool`（就是现在的 `invocation` 正则），给 chat 判断。包注释的“个人级 / 项目级”两行改成三层 |
| `internal/skills/skills.go` | `//go:embed builtin`。`Skill` 加 `Builtin bool`。`Scan`、`List` 把内置的和 `dir` 里的合起来，同名时 `dir` 里的优先，内置那份不再出现；内置的同样过 `readSkill` 的校验（name 与目录名一致等）。`Load` 和 `loadReference` 对内置的从嵌入文件读，`references/` 的限制不变；对磁盘上的读法不变（照旧 `os.ReadFile`、`os.OpenRoot`）。`List` 的每一项加 `"builtin": true/false`，内置的 `path` 写 `builtin/<名字>/SKILL.md`。导出 `BuiltinNames() []string`，给 app-server 用 |
| `internal/appserver/hub.go` | `enabledCapabilities`：除了 `skills/*/SKILL.md` 的目录名，再加上 `skills.BuiltinNames()`，去重，再去掉关掉的。`skillDelete`：磁盘上没有、内置里有这个名字时，返回 400“内置的 skill 不能删除，可以在这里关掉”。`skillCreate` 不改：和内置同名就是新建一份个人的覆盖它 |
| `internal/appserver/settings.go` | `commandList` 的注释改成三层；代码不改 |
| `internal/chat/chat.go` | `command` 的 `default`：`commands.IsInvocation(line)` 为真时 `s.turn(line)`，否则照旧“不认识的命令；/help 查看”。`/help`：在 `help` 正文后面调 `command/list`（参数 `workspace` 用 `-workspace` 的值，没给就空），列出斜杠命令；调用失败时只打一行原因，不影响其余输出 |
| `internal/web/page/composer.js` | 斜杠菜单的范围标签加上内置：`builtin` 显示“内置 · ”。没有命令时的提示改成“没有可用的命令：放在 TERN_HOME 的 commands/ 或工作区的 .agents/commands/”。文件开头那行注释同步改 |
| `internal/web/page/pages.js` | Skills 中心：`sk.builtin` 为真的卡片不显示“删除”，路径一栏显示“内置（随程序发布）”而不是 `sk.path` |
| `README.md` | “扩展点”一行：斜杠命令模板写成三层，链接改到 `internal/commands/builtin/`；`-skills`、`-skills-list` 两行：扫描的是内置的加 `skills/`；“开发 tern 本身”那段里要 `TERN_HOME=$PWD` 才能用的清单去掉 skills；在“其他入口”表前加一段“内置的斜杠命令与 skill”，写明有哪几个（`/review`、`/init`、`code-review`）、三层与覆盖、怎么关（项目级 skill 一格写“没有”，README 不写“这一期”这类 spec 的说法）；`-hooks` 一行里“问题以 `/名字` 开头时按 `commands/`、工作区 `.agents/commands/` 的模板展开”补上内置的（review 时补） |
| `AGENTS.md` | “结构”里“`skills/`、`hooks/`、`commands/` 只放真实使用的”改成：内置的命令与 skill 在 `internal/commands/builtin/`、`internal/skills/builtin/`，`hooks/` 里放 hook 脚本，都只放真实使用的 |
| `internal/commands/AGENTS.md`、`internal/skills/AGENTS.md` | 各加一条：内置的放 `builtin/`，用 `//go:embed` 编进程序，不往磁盘写 |

不改：
- `-skill`、`-skills`、`-skills-list` 的参数与用法（`-skills-list` 的输出只多一个字段）。
- `.data/hub.json` 的格式、协议版本 `Protocol`。
- `eval/cases.jsonl`（`skill-review` 这题用的就是内置的 `code-review`）。
- `hooks/`。
- `spec/` 下的文件。

## 4. 这一期不做

- 项目级 skill（`<工作区>/.agents/skills/`）。
- 命令的开关、网页端改内置的内容。
- 内置 skill 带脚本（skill 照旧只读 Markdown）。
- chat 在发出去之前先核对命令存不存在；现在和网页端一样，交给 agent 报错。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

先编一份：`T=/scratch/builtin-check && rm -rf $T && mkdir -p $T && go build -o $T/tern .`。沙箱里 `/tmp` 每次调用都是空的，后面每条命令开头都先 `T=/scratch/builtin-check`；`HOME=$T/home` 让 `~/.tern` 落在临时目录里。

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check internal/web/page/composer.js`、`node --check internal/web/page/pages.js` 通过。
2. `git status --short` 里，`commands/review.md` 和 `skills/code-review/SKILL.md` 是删除，`internal/` 下多了同样内容的两个文件：`git diff --no-index commands/review.md internal/commands/builtin/review.md` 之类的比较没有差别（文件已经移走的话，用 `git show HEAD:commands/review.md | diff - internal/commands/builtin/review.md`）。
3. 空的 `~/.tern` 里也有内置 skill：`env -u TERN_HOME HOME=$T/home $T/tern -skills-list` 的输出里有 `code-review`，`"builtin": true`。
4. 个人的覆盖内置的：
   `mkdir -p $T/home/.tern/skills/code-review && printf -- '---\nname: code-review\ndescription: 个人版\n---\n\n正文\n' > $T/home/.tern/skills/code-review/SKILL.md && env -u TERN_HOME HOME=$T/home $T/tern -skills-list`
   预期：`code-review` 只出现一次，`"builtin": false`，description 是“个人版”。验完删掉 `$T/home/.tern/skills`。
5. 内置命令能找到：`env -u TERN_HOME HOME=$T/home ARK_API_KEY=x $T/tern -question '/nosuch'; echo "exit=$?"`。假密钥只为过 `llm.LoadConfig` 的检查（`main.go:748` 在展开命令之前）。预期：不请求模型就退出，报错里列出的可用命令有 `/init`、`/review`，`exit=1`。
6. chat 的 `/help` 列出内置命令：`printf '/help\n/exit\n' | env -u TERN_HOME HOME=$T/home $T/tern chat 2>&1 | tail -20`。预期：末尾有 `/init`、`/review` 两行，标着内置。这条会在后台起 app-server，最后在同一条命令里 `env -u TERN_HOME HOME=$T/home $T/tern app-server stop`。

### Claude Code 在沙箱外做（真实模型）

7. 内置 skill 会被加载：在空的 `~/.tern`（`HOME` 指到临时目录）里，用 `-provider deepseek -skills` 问 `eval/cases.jsonl` 里 `skill-review` 那题。预期：输出里有一次 `load_skill`，`name` 是 `code-review`；回答先讲构建检查，再指出错误被忽略、文件没有关闭。
8. 内置命令在网页端和 chat 里都能用：在一个有未提交改动的临时 git 仓库上登记工作区，网页端发 `/review`，斜杠菜单里 `/review` 标着“内置”，这一轮的输入展开成 review.md 的正文，回答按严重程度列出问题；chat 里同样发 `/review`，结果一样。
9. `/init`：在一个没有 `AGENTS.md` 的临时 git 仓库（放一个小的 Go 项目）上，网页端发 `/init`。预期：根目录多了 `AGENTS.md`，里面的构建、测试命令和 go.mod 对得上，没有编出来的命令。
10. Skills 中心：内置的 `code-review` 没有“删除”，路径一栏是“内置（随程序发布）”；关掉以后，新对话的 system 里不再有它；`DELETE /hub/skills/code-review` 返回 400。

## 6. 已答复的问题（2026-10-24）

1. 内置哪些：搬现有的 `/review`、`code-review`，再加 `/init`（第 2.1 节）。
2. 仓库根目录的 `commands/`、`skills/`：搬走、删掉。
