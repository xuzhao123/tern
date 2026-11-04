# harness 基础能力补齐（对照 Codex 与 Claude Code）

状态：已实现（2026-11-03，用户要求 Claude Code 直接实现；2026-11-04 review 发现的问题由 Claude Code 按用户要求直接修，见第二节 8），分支 `harness-basics`，已合入 `main`（2026-11-04）。单元测试与真实模型（DeepSeek）运行已做；通用评测没有回归，编码评测被用户叫停，回归门槛 2026-11-04 由用户取消（见第五节）。

## 一、为什么要改

对照本机的 Codex 源码（`~/workspace/codex`，10-23 版）和 Claude Code 的做法，tern 的基础能力里差得最多的是：编码提示词很薄（`react.go` 一句“你是一个工具助手”，编码指引只有一段约 150 字，Codex 的是 7.5–24KB）；bash 每次都是新 shell，没有工作目录、单次时限；grep、glob 不认 `.gitignore`，read_file 默认只读 200 行；没有一次改多个文件的工具；子 agent 只有一种；工具不能按需加载；hook 只有 4 种事件。用户定了要做的 7 项和不做的 2 项（见第四节）。

## 二、改了什么

### 1. 提示词

- `internal/agent/react.go` 的 `SystemPrompt`：开头一句改成说明身份与回答要求（直接、先给结论、不编造工具结果）。
- `internal/workspace/workspace.go` 的 `codingGuide`（`-coding-prompt default`）：判断用户要改还是只问；先读懂再改、照周围写法；改动范围；各编辑工具什么时候用、不用 bash 改源码；不回滚用户没提交的改动、不擅自 commit/reset；从窄到宽验证、不改测试凑通过；歧义时先查再问；最终回答先说结果、写明没验证的、用 `path:行号`。参照 Codex `gpt-5.2-codex_prompt.md` 的 Editing constraints、Presenting your work 等节。`minimal`、`extended` 两个消融写法保留。

### 2. bash（照 Claude Code 的 Bash）

- 工作目录在调用之间保留：`internal/sandbox/cwd.go` 给命令包一层，开头 `cd` 到上次的目录，`EXIT` trap 把 `pwd` 写进本对话目录的 `.config/tern/cwd`，执行完读回。离开工作区和本对话目录的，结果里 `cwd_note` 说明并重置回根目录。环境变量、shell 变量和函数照旧不保留。后台命令从当前目录启动，但不改它；`Check()`（校验命令）和 `!命令` 不受影响。
- 参数 `timeout`（毫秒，最多 10 分钟；专用服务的默认时限更长时以它为准），`description`（5–10 字说明，网页端工具行优先显示它）。执行器给 bash 的时限按 `CommandLimit()` 留。
- 跨轮保留（用户 2026-11-03 定，同 Claude Code）：目录写进检查点 `state` 的 `cwd`、`cwd_host`（可以缺省的新字段，不加 `CheckpointVersion`），续聊、续跑时 `sandbox.RestoreCwd` 接上；环境信息多一个字段“bash 当前目录”，变了才在新一轮开头提醒。换了宿主机/沙箱模式、目录已不在工作区里的，从根目录开始。子 agent 和全新任务从根目录开始。

### 3. 搜索与读文件

- glob、grep 跳过 `.gitignore` 忽略的文件：每次调用跑一次 `git ls-files --others --ignored --exclude-standard --directory`（不是 git 仓库或没有 git 时退回只跳过 `.git`、`.data`、`node_modules`）。描述里写明“没列出来不代表不存在”（真实运行时 explore 子 agent 因为 glob 没列出 `dist/` 而说它不存在）。
- grep 加 `context`（0–10 行，上下文行写成 `路径-行号-内容`，段间 `--`）和 `output_mode`（content、files_with_matches、count）。
- read_file 默认读 2000 行，上限按 `ReadBudget`（1 万 token）而不是 `-tool-output-tokens`（默认 2000）；read_file 的结果和 load_skill 一样不再被转存（`context_manager.go` 的 `fullOutput`）。

### 4. apply_patch

- `internal/workspace/patch.go`：Codex 的补丁格式（Add、Delete、Update、Move to、`@@` 定位行、`*** End of File`）。先整体解析、逐个文件算出结果，有一处对不上就一个文件都不改；匹配先精确再按 `lenientLevels` 逐级放宽空白，结果里 `matched` 注明。
- 和 edit_file 一致：改、删已有文件之前要 read_file 读过且之后没被改过；新建的文件不能已存在；写入前存快照，撤销时一起退。删除在快照清单里记成 `After = "deleted"`，`PlanRewind` 据此核对。
- 权限：`permission.Decide` 对补丁里的每个路径各判一次 Edit 规则，取最严的。PostToolUse hook 对每个写入的文件核对是否被 hook 改写。网页端：工具行“应用补丁”、每轮改动卡片列出补丁里的文件（新建、删除）。
- 和 Codex 的出入：Codex 不要求先读；tern 要求，保持“改之前核对上次读到的内容”的约束。

### 5. 子 agent 类型（照 Claude Code 的 subagent）

- `internal/agent/agenttype.go`：一个 Markdown 文件一种，frontmatter `name`、`description`、可选 `tools`、`model`，正文是提示词。三层：内置 `internal/agent/agents/`（`explore`、`plan`）→ `TERN_HOME/agents/` → `<工作区>/.agents/agents/`，同名后面的覆盖前面的。
- `spawn_agent` 加 `agent_type`（枚举，描述里列出各类型的用途）；子进程带 `-agent-type`（提示词追加到系统提示词，工具收窄到 `tools`，`read_output` 保留）和类型的 `-model`。子任务ID 的哈希含类型。续跑沿用检查点里的启动参数。

### 6. 工具按需加载（照 Claude Code 的 ToolSearch）

- `internal/agent/toolsearch.go`：MCP 工具的说明合计超过 2000 token 时移出工具列表，系统提示词列名字和一句用途，加 `tool_search`（关键词或 `select:`）与 `call_tool`。
- 工具列表从第一次请求起仍不变（不在中途往列表里加工具）：被推迟的工具记在 `Deferred`，`offered` 认它们；`ExecuteBatch` 一开始把 `call_tool` 还原成原工具（同一个调用ID），hook、权限、审计、串行判断都按原工具名。

### 7. hook

- 新事件：SessionStart（matcher：startup、resume；stdout 或 additionalContext 作为提醒放在问题前面）、Notification（等人审批、`ask_user` 时；异步，不等结果）、PreCompact（matcher：manual、auto；只通知）、SubagentStop（子进程带 `-as-subagent` 时代替 Stop；matcher 是子 agent 类型）、SessionEnd（这一轮结束，带 reason）。
- 新输出：任何事件返回 `"continue": false`（和 `stopReason`）让这一轮停下（PreToolUse 不执行该工具；PostToolUse 这一批做完、检查点写好后停，原因 `hook_stop`）；`systemMessage` 作为 `hook/completed`（结局 `message`）显示给人，不进模型上下文。

### 8. review 后的修正（2026-11-04）

- 沙箱外的 git 不执行仓库配置：`ignored` 新增的 `git ls-files` 会运行 `.git/config` 的 `core.fsmonitor`，`git status`、`git diff` 还会运行 `filter.*.clean`，而模型能用编辑工具写 `.git/config`，或在非 git 工作区用 bash 造一个 `.git`。`internal/workspace/git.go` 的 `Git` 统一走私有 git 目录（`SafeGitEnv`，从 `appserver/gitread.go` 移过来，两边共用），`ignored`、环境信息、`verify_feature` 的 diff 都改用它。全局配置同样不读，所以全局的 `core.excludesFile` 不再生效；用了 clean filter 的文件（如 Git LFS）在环境信息里可能显示成已修改。
- edit_file、write_file、apply_patch 不能写 `.git`（路径里任何一段，不分大小写；经符号链接进去的、悬空的符号链接也拒绝）。
- `hooks/hooks.json` 的编辑 hook 匹配 apply_patch；`hooks/edit-paths.jq` 取出这次涉及的路径，`protect-generated.sh`、`gofmt.sh`、`js-check.sh` 逐个处理。PreToolUse 拦下一个文件时整份补丁都不应用。
- apply_patch：整份 CRLF 的文件写回时保留 CRLF（混用换行的文件，没碰到的行原样，碰到的行和补丁一致）；`@@ 定位行` 后面只有 `+` 行时加在定位行后面（Codex 加在文件末尾，这里不跟；没有定位行时仍加在末尾）；没有定位行时原文要唯一，同一放宽级别下出现多处就报错，比 Codex 严，有定位行时取它后面的第一处。
- apply_patch 的边界：文件结尾的状态不变（没有结尾换行的还是没有，末尾空行还在）；同一份补丁新建的路径互相冲突（`a` 和 `a/b`）时动手前就拒绝；改名沿用原文件的权限；快照清单记下原来的权限（`Change.Mode`，可以缺省），删除、改名撤销后可执行位回来。
- 沙箱造不出工作区根目录的 `.git`（Linux）：不是 git 仓库时，可写的工作区里占一个空的只读 tmpfs，挂载点在每条前台命令结束和进程退出时收走（`unmaskGit`，只删空目录）；worktree 的 `.git` 文件也只读。代价：非 git 工作区里沙箱的 `git init`（含 `!git init`）会失败，要用就换完全访问。macOS 的 Seatbelt 规则按路径拒绝写 `WORKSPACE_GIT`，不存在时同样生效，代码没改，这一条只读过规则、没有实测。
- `internal/eval/coding.go` 的 `saveDiff`（模型工作过的目录）走 `SafeGitEnv`。建试次的 git 命令在模型开始之前跑，不用改。
- `tool_search` 的 `select:` 区分大小写，用原文匹配（MCP 工具名可能带大写）。

## 三、不要改的

- 工具列表在一轮里固定、也是允许列表；hook 只来自用户；沙箱的挂载与网络规则。
- `edit_file`、`write_file` 的行为。

## 四、这一期不做

- Messages 协议的提示缓存（`cache_control`）：用户不直连 Claude，暂缓。
- 改完代码后的诊断（LSP）：用户定先不做。

## 五、验收

已做：

1. `gofmt`、`go vet ./...`、Linux 与 `GOOS=darwin` 的 `go build ./...`、`go test ./...` 全过；页面脚本 `node --check` 全过。新增单元测试：`internal/workspace/patch_test.go`（一份补丁里新建、多段修改、末尾段、宽松匹配、删除、改名；对不上时一个都不改；撤销全部还原）、`internal/permission/permission_test.go`（补丁多路径取最严）、`internal/agent/toolsearch_test.go`、`internal/agent/agenttype_test.go`、`internal/hooks/hooks_test.go`（真实 sh 命令：matcher、continue:false、SessionStart 上下文）。
2. bash 工作目录：宿主机模式下实际跑了一串命令（`cd sub` 之后下一条在 sub、`exit 3` 不影响记录、`cd /etc` 被重置、timeout 1 秒终止、超上限报错、后台命令）。`internal/sandbox/cwd_test.go` 用真实 bash 模拟“这一轮存进检查点、下一轮新进程读回”，以及换模式后从根目录开始。
3. 网页端加真实模型：`spec/web-product/check-bash-cwd.py`（已加进 `run-checks.py`，`--only bash-cwd`），沙箱（bwrap）和宿主机各一条对话：第一轮只 `cd sub/deep`，检查点 `state.cwd` 为 `/workspace/sub/deep`（宿主机是真实路径）；第二轮模型只跑 `pwd`，输出就是那个目录；第二轮的请求里环境信息带“bash 当前目录”。2026-11-03 通过。
4. 真实模型（`-provider deepseek`，临时 git 仓库，只开文件工具）：一次 `apply_patch` 改两处并新建文件；grep 没搜到 `.gitignore` 里 `dist/` 下的同名函数；最终回答按新提示词写明“没跑构建”和 `path:行号`。`-subagents` 时父 agent 选了 `explore`，子进程工具收窄为 5 个、4 次请求返回结论。hook：SessionStart 的输出被模型照做；PostToolUse 返回 `continue:false` 后这一轮以 `hook_stop` 停下，SessionEnd 收到 `reason=hook_stop`。

5. 第二节 8 的修正：`internal/workspace/git_test.go`、`patch_test.go` 的新增用例。真实模型（DeepSeek，临时 `TERN_HOME`）：带 `hooks/hooks.json` 的 git 仓库里，CRLF 文件 `apply_patch` 后仍是 CRLF；`@@ func a() {` 只加一行时落在定位行后面并被 gofmt 改写；改 `DO NOT EDIT` 文件的补丁被 hook 拒绝；`write_file` 写 `.git/config` 被拒绝。非 git 工作区里沙箱 bash 造出带 fsmonitor 的 `.git` 后再 `glob`，宿主机上的标记文件没有出现。一份补丁同时改没有结尾换行的文件、带末尾空行的文件、改名可执行脚本、删文件，结果都符合上一节；新建 `p` 和 `p/q` 的补丁报冲突，两个都没建。真实 bwrap（`-shell`）：非 git 工作区 `mkdir .git`、写 `.git/config` 都报只读，其他文件照常写，结束后宿主机上没有 `.git`；真仓库 `git status`、`git log` 正常、配置不能改；worktree 的 `.git` 文件不能改不能删；plan 模式照常启动。真实模型：后台命令和前台命令都造不出 `.git`，结束后没有留下挂载点。`internal/eval/coding_test.go` 覆盖 `saveDiff`。

没做到的：

- 用户取消了回归门槛（2026-11-04），修正后没有再跑评测，下面两条是修正之前的数据。“定位不唯一就报错”会多一些补丁重试，影响没有量过。
- 通用评测 `e20261103-162726`（基线 `e20261031-165502`）：pass@1 95%，逐题配对差 +0.0 个百分点，没有题变差。
- 编码评测 `e20261103-162721`（基线 `e20261031-165716`）：跑到 50/60 次时被用户叫停（一次全套约 ¥80–90，主要花在 `spec-*` 题上，3.16 亿 token 里 98% 是缓存命中）。没有和基线比。用户定：没有用户的明确指令，不跑评测。
- 按需加载没有用真实的大 MCP server 跑过，只有单元测试。

## 六、需要确认的问题

0. 子目录里的 `.git`（如 `sub/.git`）沙箱 bash 仍然造得出：测试夹具等正常用途也会这样做，挂载也挡不住任意路径。harness 不会在那里跑 git，但人进那个目录跑 git 时会执行里面的配置。先不处理，要不要收紧？

1. 编码提示词变长后，评测里请求次数和 token 会变多；如果 pass@1 没升，是保留新提示词，还是收回一部分？
