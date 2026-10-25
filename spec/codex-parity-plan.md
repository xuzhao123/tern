# 和 Codex 对齐的任务清单

2026-10-24 用户说：接下来进一步和 Codex 的体验对齐，这份清单就是任务列表。同一天又定了三件事：
- **命令行界面（chat 做成 TUI）先不做，重点做网页端的体验**；工作区的添加方式、SSH 的连接方式也要对齐。
- 计划做好以后直接交给 tern 干，**tern 全部干完再统一 review**，不必每条先等用户确认。

- **怎么用**：做某一条之前，先在 `spec/` 写那一条的设计，把文件名填进“spec”一列，状态改成“设计中”；交给 tern 实现、验收合并后改成“已完成”。顺序按优先级来，用户可以随时调整。
- **对照的 Codex**：本机 `~/workspace/codex`，提交 `970111f6cb`（2026-10-23）。下文的 Codex 路径都相对 `codex-rs/`。Codex 的桌面端、手机端不开源，它们能做什么，从 app-server 协议（`app-server-protocol/src`）里的方法推断，例如 `project/list`、`fs/readDirectory`。
- **优先级**：
  - P0：天天用、现在就别扭的。
  - P1：常用、差距明显的。
  - P2：有了更好，或者工作量大、要先想清楚的。
- **状态**：未开始 / 设计中 / 进行中 / 已完成 / 暂缓。

## 0. 已经做的

| 项 | Codex | 状态 | spec |
| --- | --- | --- | --- |
| 数据目录默认 `~/.tern`，`TERN_HOME` 只用来换目录 | `~/.codex` + `CODEX_HOME` | 已完成 | [tern-home-default.md](tern-home-default.md) |
| 沙箱的 Go 工具链按工作区的 `go.mod` 选 | 命令在工作区里跑 | 已完成 | 同上第 7 节 |
| 登记工作区：`~` 展开，其他相对路径提示填绝对路径 | — | 已完成 | 同上第 8 节 |
| 内置斜杠命令 `/review`、`/init` 与内置 skill；个人、项目里同名的覆盖；chat 里也能用斜杠命令 | 内置 `/review`、`/init`，`~/.codex/prompts`、skills | 已完成 | [builtin-commands-skills.md](builtin-commands-skills.md) |
| 去掉 `calculator`、`get_current_datetime`；日期放进环境信息 | 默认没有这两类工具；`current_date`、`timezone` 在环境上下文里（`core/src/context/world_state/environment.rs`） | 已完成 | [remove-legacy-tools.md](remove-legacy-tools.md) |
| 网页端插话：马上显示在待发送区，送到模型时挪进对话，没赶上的接着发，“立即发送” | `tui/src/bottom_pane/pending_input_preview.rs`、`tui/src/chatwidget.rs:1368` | 已完成 | [steer-like-codex.md](steer-like-codex.md) |

## 1. 工作区（项目）

Codex 把工作目录当“项目”管：`project/list`、`create`、`import`、`read`、`update`、`move`、`delete`，对话属于某个项目（`thread/project/updated`）。界面靠 `fs/readDirectory`、`fs/getMetadata`、`fs/createDirectory` 浏览所连机器上的目录来选，远端也一样。

tern 现在：设置页里一个输入框，手敲路径（`internal/web/page/pages.js:135`）；必须是 git 仓库；名字取目录名；新对话只能从已登记的里选，没登记的要先去设置页。

| # | 功能 | Codex 怎么做 | tern 现在 | 优先级 | 状态 | spec |
| --- | --- | --- | --- | --- | --- | --- |
| 1.1 | 浏览目录来选 | `fs/readDirectory` 浏览所连机器（本机或远端）的目录，点选打开 | 只能手敲路径，敲错了只有一句报错 | P0 | 已完成 | [workspace-picker.md](workspace-picker.md) |
| 1.2 | 在新对话里直接添加 | 打开文件夹就是一个项目，不用先去设置 | 新对话的工作区下拉里只有已登记的；要先去设置页登记，再回来选 | P0 | 已完成 | [workspace-picker.md](workspace-picker.md) |
| 1.3 | 候选与最近用过的 | 项目列表就是用过的目录 | 没有候选，每个都要自己想路径 | P1 | 已完成 | [workspace-more.md](workspace-more.md) |
| 1.4 | 对话按项目分组、筛选 | 对话挂在项目下，还能分组（`threadSection/*`） | 左侧列表按主机分组，不按工作区分 | P1 | 已完成 | [workspace-more.md](workspace-more.md) |
| 1.5 | 工作区改名 | `project/update` | 名字取目录名，重名加 `-2`，不能改；能移除 | P2 | 已完成 | [workspace-more.md](workspace-more.md) |
| 1.6 | 不是 git 仓库的目录 | 可以用（`codex exec` 要加 `--skip-git-repo-check`，TUI 先问要不要信任） | 直接拒绝（“改坏了要能用 git 回退”）；可以考虑给个“git init”按钮 | P2 | 已完成 | [workspace-more.md](workspace-more.md) |
| 1.7 | 在 worktree 里开对话 | `/worktree`：新开一个 git worktree 跑这条对话，不碰当前目录 | 没有；tern-implement 是手动 clone | P2 | 设计中 | [copy-workspace.md](copy-workspace.md) |

## 2. SSH 远程

Codex：远端跑一个常驻的 daemon。`codex app-server daemon bootstrap` 在新机器上一条命令装好程序包、写好设置、起后台服务（`app-server-daemon/README.md`），还能自动更新。客户端经 SSH 连这个 daemon，TUI 也认 SSH 转发过来的 socket（`tui/src/startup_orchestration.rs:109`）。另有不走 SSH 的 `remoteControl/*` 配对，靠 OpenAI 的服务中转，tern 不跟。

tern 现在（README“远程”一节）：每个界面各自起 `ssh -T -o BatchMode=yes <别名> 'exec "$SHELL" -lc "tern app-server proxy"'`。远端要自己装好 tern、在登录 shell 的 PATH 上、`TERN_HOME` 里有 `.env`。第一次连某台机器要先在终端里手动 `ssh` 一次确认指纹。网页端的主机菜单列 `~/.ssh/config` 里的别名，连不上时显示 ssh 的报错原文。

| # | 功能 | Codex 怎么做 | tern 现在 | 优先级 | 状态 | spec |
| --- | --- | --- | --- | --- | --- | --- |
| 2.1 | 远端一键装好、版本跟上 | `daemon bootstrap` 把程序装到远端、起服务；`daemon update` 更新 | 要登录远端手动装 tern、配 `~/.profile`；两边协议版本不一致时只能手动重装。可以考虑：远端没有或版本不对时，本机经 ssh 把自己的程序拷过去（同架构时），放进远端的 `~/.tern/bin`，proxy 直接用这个路径，不依赖 PATH | P1 | 设计中 | [remote-setup.md](remote-setup.md) |
| 2.2 | 连不上时说清是哪一步、怎么办 | daemon 的命令都输出一行 JSON，客户端按字段判断 | 只显示 ssh 的报错原文；看不出是认证失败、指纹没确认、远端找不到 tern、版本不一致，还是远端没放密钥 | P1 | 设计中 | [remote-setup.md](remote-setup.md) |
| 2.3 | 第一次连接在网页里确认指纹 | 桌面端自己处理 | 必须先去终端手动 `ssh` 一次，否则 `BatchMode` 直接失败 | P2 | 设计中 | [remote-setup.md](remote-setup.md) |
| 2.4 | 远端的模型密钥 | 远端 daemon 用自己的登录状态 | 远端的 `TERN_HOME` 里必须有 `.env`。要不要让远端的模型请求绕回本机、密钥只放本机，需要先想清楚 | P2 | 设计中 | [remote-setup.md](remote-setup.md) |
| 2.5 | 远端的工作区也能浏览着选 | 同 1.1，`fs/readDirectory` 走的是所连的机器 | 在远端主机的设置页里手敲远端路径 | P0（随 1.1） | 已完成（远端由用户测） | [workspace-picker.md](workspace-picker.md) |

## 3. 网页端交互

| # | 功能 | Codex 怎么做 | tern 现在 | 优先级 | 状态 | spec |
| --- | --- | --- | --- | --- | --- | --- |
| 3.1 | 排队的后续消息（和插话分开） | TUI 里 Enter 是插话，Tab 是排到这一轮结束后再发（`tui/src/bottom_pane/chat_composer.rs:835`）。队列在服务端（`thread/queue/add`、`update`、`delete`、`reorder`），换个客户端也看得到、能改顺序 | 运行中只有插话；没赶上的插话会接着发，但待发送区只在发出它的那个页面里 | P1 | 已完成 | [queued-followups.md](queued-followups.md) |
| 3.2 | 改前面的问题重新发 | Esc Esc 回到上一条自己的消息，改了重发，后面的作废（`tui/src/app_backtrack.rs`）；协议里有 `thread/revert` | 有撤销（三种选法）和分叉，但没有“改这句重发”一步到位 | P1 | 已完成 | [edit-resend.md](edit-resend.md) |
| 3.3 | 每一轮的改动 diff | 改文件时显示 diff；`/diff` 看整个工作区，包括未跟踪的文件 | 工具卡片里能看到单次编辑；没有“这一轮改了哪些文件”和整个工作区的 diff 视图 | P1 | 已完成 | [workspace-diff.md](workspace-diff.md) |
| 3.4 | `@` 提到文件 | 输入 `@` 模糊搜索工作区里的文件（`fuzzyFileSearch`），选中插入路径 | 没有 | P1 | 已完成 | [at-mention.md](at-mention.md) |
| 3.5 | 推理强度 | 和模型一起选（`/model`） | 命令行有 `-reasoning-effort`，网页端没有入口 | P1 | 已完成 | [effort-picker.md](effort-picker.md) |
| 3.6 | 跑完、要审批时通知 | TUI 失焦时发终端通知；`notify` 配一个程序，每轮结束调用（`core/src/config/mod.rs:798`） | 页面不在前台时什么都不提示，审批要人自己回来看 | P1 | 已完成 | [notify-unread.md](notify-unread.md) |
| 3.7 | 未读标记 | `thread/readState/*`：哪些对话有没看过的新结果 | 没有 | P2 | 已完成 | [notify-unread.md](notify-unread.md) |
| 3.8 | Esc 打断 | Esc | 只有停止按钮 | P2 | 设计中 | [web-esc-copy-export.md](web-esc-copy-export.md) |
| 3.9 | 复制回答、导出对话 | `/copy` 复制最后的回答；`/export` 导出成 Markdown | 有复制按钮（`internal/web/page/util.js:141`，复制的是哪些内容待确认）；没有导出 | P2 | 设计中 | [web-esc-copy-export.md](web-esc-copy-export.md) |
| 3.10 | 旁支对话 | `/side`：临时分叉出去问一句，不污染主对话 | 只有正式的分叉 | P2 | 设计中 | [side-question.md](side-question.md) |
| 3.11 | 在界面里跑一条命令 | `!命令`（`thread/shellCommand`），输出进对话上下文；`command/exec` | 没有 | P2 | 设计中 | [shell-command.md](shell-command.md) |
| 3.12 | 后台终端 | `thread/backgroundTerminals/list`、`terminate`：看、停后台命令 | 后台任务只有模型能管 | P2 | 设计中 | [background-jobs-panel.md](background-jobs-panel.md) |
| 3.13 | 评审的几种范围 | `review/start`：未提交的改动、对比某个分支、某个提交、自定义说明 | 内置 `/review` 只看未提交的改动 | P2 | 设计中 | [review-scope.md](review-scope.md) |

## 4. 会话管理

| # | 功能 | Codex 怎么做 | tern 现在 | 优先级 | 状态 | spec |
| --- | --- | --- | --- | --- | --- | --- |
| 4.1 | 改名、归档、删除 | `thread/name/set`、`thread/archive`、`unarchive`、`delete` | 都没有：网页端没有这些路由（`internal/web/server.go:150` 起），列表只增不减，标题就是第一句话 | P0 | 已完成 | [conversation-manage.md](conversation-manage.md) |
| 4.2 | 搜索对话 | `thread/search`、`thread/searchOccurrences` | 没有 | P1 | 已完成 | [thread-search.md](thread-search.md) |
| 4.3 | 长任务的目标 | `thread/goal/set`：给长任务定一个目标，跑偏了能看出来 | 有进度文件和 `verify_feature`，但界面上看不到“目标是什么、做到哪了” | P2 | 设计中 | [thread-goal.md](thread-goal.md) |

## 5. 指令与配置

| # | 功能 | Codex 怎么做 | tern 现在 | 优先级 | 状态 | spec |
| --- | --- | --- | --- | --- | --- | --- |
| 5.1 | 全局个人指令 | `~/.codex/AGENTS.md` 对所有项目生效，放在项目的 `AGENTS.md` 前面 | 只读工作区根目录和子目录的 `AGENTS.md`/`CLAUDE.md`（`internal/workspace/context.go:51`），`~/.tern/AGENTS.md` 不生效 | P0 | 已完成 | [personal-agents-md.md](personal-agents-md.md) |
| 5.2 | 项目级配置 | 仓库里的 `.codex/config.toml`，信任这个项目以后生效 | 设置只有平台层和个人层；项目级只有 `.agents/commands/` | P2 | 设计中 | [project-config.md](project-config.md) |

## 6. 沙箱与工具

| # | 功能 | Codex 怎么做 | tern 现在 | 优先级 | 状态 | spec |
| --- | --- | --- | --- | --- | --- | --- |
| 6.1 | macOS 上的沙箱 | macOS 用 Seatbelt | 只有 Linux 的 bwrap；在 Mac 上直接跑 tern 没有 bash（现在靠 Lima 虚拟机） | P2 | 设计中 | [macos-sandbox.md](macos-sandbox.md) |
| 6.2 | 交互式命令 | `exec_command` 起 PTY 会话，`write_stdin` 往里写（`core/src/tools/handlers/unified_exec.rs`） | `bash` 一次一条，`bash_output`、`bash_kill` 管后台任务，不能往进程里写输入 | P2 | 设计中 | [bash-interactive.md](bash-interactive.md) |
| 6.3 | MCP 资源 | `list_mcp_resources`、`read_mcp_resource`（`core/src/tools/handlers/mcp_resource.rs`） | 只接 MCP 的 tools，不接 resources（`internal/mcp/mcp.go`） | P2 | 设计中 | [mcp-resources.md](mcp-resources.md) |
| 6.4 | MCP 登录 | `/mcp login <名字>`，远程 MCP 的 OAuth（`rmcp-client/src/oauth_http_client.rs`） | 没有 OAuth，只能用不需要登录的 server | P2 | 设计中 | [mcp-oauth.md](mcp-oauth.md) |
| 6.5 | 把 tern 当 MCP server | `codex mcp-server`：别的客户端把 Codex 当一个工具调用 | `-mcp-serve` 只暴露计算器 | P2 | 设计中 | [tern-mcp-server.md](tern-mcp-server.md) |

## 7. tern 有、但做得不如 Codex 的

都是 `spec/README.md` “还没写设计的待办”里记过的，影响 tern 干活的质量。

| # | 问题 | Codex | 优先级 | 状态 | spec |
| --- | --- | --- | --- | --- | --- |
| 7.1 | `edit_file` 匹配失败时只报错，模型要多花几轮去看原文（gofmt 对齐过的空白最常见） | `apply_patch` 按上下文匹配，容错更好 | P1 | 已完成 | [edit-file-lenient.md](edit-file-lenient.md) |
| 7.2 | 重复动作检测误判“改了文件、重编、再跑同一条命令” | —（Codex 有没有类似的拦截还没核对） | P1 | 已完成 | [loop-and-progress-fixes.md](loop-and-progress-fixes.md) |
| 7.3 | 沙箱里跑无头 Chrome，后台流量（google.com 等十来个域名）每个都弹一次联网审批（2026-10-24 实现插话时又碰到 6 个） | 沙箱默认断网，不逐个问 | P1 | 已完成 | [sandbox-quiet-chrome.md](sandbox-quiet-chrome.md) |
| 7.4 | 模块缓存目录不存在时整个 bash 沙箱起不来 | — | P2 | 已完成 | [sandbox-quiet-chrome.md](sandbox-quiet-chrome.md) |
| 7.5 | 进度文件里写错的校验命令，tern 自己改不了 | — | P2 | 已完成 | [loop-and-progress-fixes.md](loop-and-progress-fixes.md) |

## 8. 暂缓：命令行界面

2026-10-24 用户定：命令行界面先不做，重点做网页端。下面这些等网页端做完再看。到时还要先定一件事：chat 做成全屏 TUI（像 Codex 默认那样占满终端），还是内联模式（底部固定输入框和状态行，上面照常滚动，像 Codex 的 `--no-alt-screen`、Claude Code）。

| # | 功能 | Codex 怎么做 | tern 现在 | 状态 |
| --- | --- | --- | --- | --- |
| 8.1 | 输入框：行编辑、历史、多行、粘贴 | `tui/src/bottom_pane/chat_composer.rs` | chat 用 `bufio` 读一行，不能改字、没有历史，粘贴多行会拆成好几句 | 暂缓 |
| 8.2 | 状态行、Esc 打断、待发送区 | `Working (12s • esc to interrupt)` | 只有流式输出，Ctrl+C 停止 | 暂缓 |
| 8.3 | `/status`、`/model`、`/diff`、`/mcp`、`/skills`、`/hooks` | 斜杠命令（`tui/src/slash_command.rs`） | 没有 | 暂缓 |
| 8.4 | `@` 提到文件、`!` 跑本机命令、附图 | 同上 | 没有 | 暂缓 |
| 8.5 | Markdown 与 diff 渲染 | 带颜色渲染 | 原样打印 | 暂缓 |
| 8.6 | 续聊时从列表选对话 | `codex resume` 默认弹列表、可搜，`--last` 接最近一条；`codex fork` 同理 | 要 `-continue <对话ID>`，得先去网页端抄 ID | 暂缓 |
| 8.7 | 机器可读的输出 | `codex exec --json` 每个事件一行 JSONL，`-o` 把最后的回答写进文件（`exec/src/cli.rs`） | `-question` 只打给人看的文字 | 暂缓 |
| 8.8 | 从标准输入读问题、规定回答格式 | 不给问题或给 `-` 时读 stdin；`--output-schema` | `-question` 必须给 | 暂缓 |
| 8.9 | 非交互续聊、往已有对话发消息 | `codex exec resume --last`、`codex queue` | 没有 | 暂缓 |
| 8.10 | 配置文件与 profile | `~/.codex/config.toml`，`-c key=value` 临时覆盖 | 命令行全靠参数；网页端的设置只在网页端改 | 暂缓 |
| 8.11 | shell 补全、自检 | `codex completion`、`codex doctor` | 没有 | 暂缓 |

## 9. 不打算做

Codex 有，但和 tern 的用法无关，或者依赖 OpenAI 自己的服务：
- 账号与用量：`/logout`、`/usage`、限额、`codex login`。
- `remoteControl/*` 的配对（靠 OpenAI 的服务中转）；tern 走 SSH。
- 插件和应用市场：`/plugins`、`/apps`。
- 语音、`/pets`、TUI 主题、状态栏与标题的配置、Vim 模式、按键映射。
- 图片生成、云端任务、交给桌面 App 接着做（`/app`）、自动更新、实验特性开关。
- Windows 沙箱。

## 需要确认的问题

1. 2.1“本机把程序拷到远端”：按建议做了，两边系统和架构相同时才拷，不同时提示在远端安装（remote-setup.md）。
2. 2.4 远端的模型密钥：做成“显示远端有没有配密钥 + 一键把本机的密钥复制过去”；“密钥只放本机、请求绕回本机”不做，因为要让 app-server 反过来请求界面，架构上定了不做（remote-setup.md 2.4）。
3. 3.9 网页端现有的复制按钮是观测页“原始输入 / 原始输出”卡片上的，复制的是模型请求的原始数据，不是回答；“复制回答”另做（web-esc-copy-export.md）。
