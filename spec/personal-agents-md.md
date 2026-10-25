# 全局个人指令 ~/.tern/AGENTS.md

状态：已实现（2026-10-24，tern 实现；Claude Code 用真实模型和无头 Chrome 驱动真实页面验收）。

## 1. 为什么要改

2026-10-24 用户想像 Codex 一样把自己的东西放进 `~/.tern`。Codex 的 `~/.codex/AGENTS.md` 对所有项目生效，排在项目的 `AGENTS.md` 前面。tern 只读工作区根目录和子目录里的 `AGENTS.md`/`CLAUDE.md`（`internal/workspace/context.go:51`），而且只在选了工作区时才放进系统提示词（`internal/agent/react.go:958`）。`~/.tern/AGENTS.md` 写了也不生效，没选工作区的对话更是什么约定都没有。

## 2. 改什么

### 2.1 读哪个文件

- `$TERN_HOME/AGENTS.md`，默认就是 `~/.tern/AGENTS.md`。只认这一个名字，不读 `CLAUDE.md`。
- 和项目指令一样：二进制文件不读；超过 32KB 截断并注明（`maxDocBytes`）；去掉首尾空白后是空的，就当没有。
- 如果 `TERN_HOME` 就是这次的工作区根目录（开发 tern 时 `TERN_HOME=$PWD`），这个文件已经作为项目指令读了，不再当个人指令读一遍。
- agent 每轮都是新进程，启动时读一次。

### 2.2 放在哪

- 系统提示词里，放在项目指令前面。**不管有没有选工作区都放**：

  ```
  # 个人指令（<文件的绝对路径>）
  以下是用户本人写给 agent 的约定，适用于所有对话；和后面的项目指令冲突时以项目指令为准，和上面的系统规则冲突时以系统规则为准。

  <正文>
  ```

- 续聊沿用第一轮的系统提示词。文件之后改过，就照项目指令的做法（`react.go:1004`）在问题前面追加一条提醒：“个人指令在上一轮之后改过，以这一份为准：…”。
- 子 agent（`spawn_agent`）也是同一个 `TERN_HOME` 下的 agent 进程，照常读到。

### 2.3 评测不读

评测要的是可比较的结果，不能混进个人偏好。
- agent 加参数 `-personal-instructions`，默认 true；为 false 时不读。
- 评测给每个试次的公共参数（`internal/eval/run.go:349` 的 `common`）加上 `-personal-instructions=false`。
- 任务队列（`tern queue`）是用户自己的任务，照常读。

### 2.4 设置页显示一行

- `settings/read` 的返回多一项 `instructions: {path, exists, chars}`，是 app-server 那台机器上的 `$TERN_HOME/AGENTS.md`。远端主机的设置页显示的就是远端的。
- 设置页在“工作区”那一节上面加一行：
  - 文件存在时：“个人指令：<path>（<chars> 字），每个对话都会带上；直接编辑这个文件，下一个新对话生效。”
  - 不存在时：“个人指令：<path> 还不存在。写在这里的约定每个对话都会带上（类似 ~/.codex/AGENTS.md）。”
- 只显示，不在网页上编辑。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/workspace/context.go` | 加读个人指令的函数（复用 `doc` 里截断与二进制判断的写法，路径用 `$TERN_HOME/AGENTS.md`，和工作区根目录相同时返回空），以及 `PersonalInstructions()` 返回放进系统提示词的那一段；文件开头的注释补上个人指令这一样 |
| `internal/agent/react.go` | `SystemPrompt`：个人指令放在 `workspace.Rules() + workspace.Instructions()` 前面，并且不受 `workspace.Dir != ""` 的条件限制；`:1004` 旁边对个人指令做同样的“改过就追加提醒” |
| `main.go` | 加 `-personal-instructions` 参数（默认 true），false 时不读；写进检查点的启动参数照旧（续跑保持一致） |
| `internal/eval/run.go` | 试次的公共参数加 `-personal-instructions=false` |
| `internal/appserver/settings.go` | `settings/read` 的返回加 `instructions` |
| `internal/web/page/pages.js` | 设置页加那一行 |
| `README.md` | 快速开始讲数据目录的那段补一句：`~/.tern/AGENTS.md` 是个人指令，所有对话都会带上，排在项目的 `AGENTS.md` 前面；评测不读。参数表加 `-personal-instructions` |
| `AGENTS.md` | “数据与密钥”或“结构”里补一句：`$TERN_HOME/AGENTS.md` 是用户的个人指令，评测不读 |

不改：项目指令的读法（根目录 `AGENTS.md` 优先，没有读 `CLAUDE.md`；子目录按需加载）、`spec/` 下的文件。

## 4. 这一期不做

- `AGENTS.override.md` 这类覆盖文件。
- 在网页上编辑个人指令。
- 按账号分开的个人指令（这一版只有平台空间，一个 `TERN_HOME` 一份）。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check internal/web/page/pages.js` 通过。
2. 读代码报告：系统提示词里个人指令和项目指令的先后；没选工作区时也有个人指令；`-personal-instructions=false` 和 `TERN_HOME` 等于工作区根目录时都不读。

### Claude Code 在沙箱外做（真实模型，临时 `HOME`）

3. 在临时的 `~/.tern/AGENTS.md` 写一句“每次回答的最后一行写：—— 来自个人指令”。`-provider deepseek -question '1+1 等于几？'`（没选工作区）的回答最后一行是那句话；检查点里的系统提示词有“# 个人指令”一段。
4. 选了一个根目录有 `AGENTS.md` 的工作区：系统提示词里个人指令在项目指令前面。
5. 续聊之前改一下这个文件：下一轮的请求里多一条“个人指令在上一轮之后改过”的提醒。
6. 评测：跑一题 `tern eval -case weekday -k 1`，试次的请求里没有“# 个人指令”。
7. 设置页显示那一行（文件存在、不存在两种）。
