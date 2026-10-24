# 设计文档

新功能先在这里写设计，确认后交给 tern 实现（分工见 [AGENTS.md](../AGENTS.md)，交给 tern 的步骤见 [.claude/skills/tern-implement](../.claude/skills/tern-implement/SKILL.md)）。一份设计一个 Markdown 文件，结构参照已有的几份：为什么要改、改什么、按文件的改动（含不要改的）、这一期不做什么、验收。只有用户要求时才另配 HTML 页面，两者内容一致，有出入以 Markdown 为准。

| 文档 | 内容 | 状态 |
| --- | --- | --- |
| [codex-parity-plan.md](codex-parity-plan.md) | **任务清单**：和 Codex 对齐的功能点，哪些没做、哪些做得不好，按优先级排；做某一条前先写它的 spec，填回这里 | 清单（2026-10-24），持续更新 |
| [app-server-design.md](app-server-design.md) | 照 Codex 的 SSH 远程：常驻的 `tern app-server` 管对话，网页端、终端、以后的桌面端经 Unix socket 或 SSH 连它；`TERN_HOME` | 第一步（`TERN_HOME`、`-agent-server`）已实现（2026-10-23）；第一期（app-server、网页端与 chat 改成客户端、去掉租户账号、SSH 远程与重连）已实现（2026-10-24）。第二期另写在 app-server-phase2-design.md，已实现（2026-10-23） |
| [app-server-phase2-design.md](app-server-phase2-design.md) | app-server 第二期：`thread/fork` 与 chat 的 `/fork`、网页端的“分叉”；`thread/rewind` 只退文件或只退对话，网页端的撤销可选三种；一个网页端同时连几台主机（读 `~/.ssh/config`，记住连过的，列表按主机分组） | 已实现（2026-10-23） |
| [app-server-auto-replace.md](app-server-auto-replace.md) | 改了代码重新起界面就行：界面发现后台的 app-server 是另一个程序、又比自己先启动，就换成自己的程序；正在跑的对话由新的接管，有评测在跑时不换 | 已实现（2026-10-23，Claude Code 直接实现） |
| [tern-home-default.md](tern-home-default.md) | 数据目录默认就是 `~/.tern`（同 Codex 的 `~/.codex`）：去掉“源码根目录里就用当前目录”的特例，`TERN_HOME` 只用来换目录；`-dev`、临时 clone 的验证显式设 `TERN_HOME`；沙箱的 Go 工具链改按工作区的 `go.mod` 选 | 已实现（2026-10-24） |
| [builtin-commands-skills.md](builtin-commands-skills.md) | 内置的斜杠命令与 skill：`/review`、`/init`、`code-review` 用 `//go:embed` 编进程序，个人、项目里同名的覆盖；chat 里也能用斜杠命令 | 已实现（2026-10-24） |
| [remove-legacy-tools.md](remove-legacy-tools.md) | 去掉 learning-agent 留下的 `calculator`、`get_current_datetime`（对照 Codex 的默认工具）；日期（Asia/Shanghai）一律放进环境信息；网页端的计算器 MCP 保留；评测集删“计算”组 | 已实现（2026-10-24） |
| [steer-like-codex.md](steer-like-codex.md) | 网页端插话照 Codex：发出去马上显示在输入框上方的待发送区，送到模型时进对话（`client_id` 认回执），没赶上这一轮的接着发，“立即发送”；修好网页端丢插话；文案 | 已实现（2026-10-24） |
| [workspace-picker.md](workspace-picker.md) | 浏览目录来登记工作区（`workspace/browse`），新对话的工作区下拉里直接“＋ 添加工作区…”；远端浏览远端的目录（计划 1.1、1.2、2.5） | 已实现（2026-10-24） |
| [conversation-manage.md](conversation-manage.md) | 对话改名、归档、删除：名字与归档放 `.meta.json`，删除不动被分叉对话引用的轮次文件（计划 4.1） | 已实现（2026-10-24） |
| [personal-agents-md.md](personal-agents-md.md) | 全局个人指令 `~/.tern/AGENTS.md`：每个对话都带上，排在项目指令前面；评测不读（计划 5.1） | 已实现（2026-10-24） |
| [sandbox-quiet-chrome.md](sandbox-quiet-chrome.md) | 沙箱里的 `chrome` 包装成固定关掉后台联网；Chrome 后台域名直接拒绝；模块缓存目录不存在时沙箱照常起（计划 7.3、7.4） | 已实现（2026-10-24） |
| [edit-file-lenient.md](edit-file-lenient.md) | `edit_file` 对不上原文时逐级放宽空白；真对不上就给出最接近的原文（计划 7.1） | 已实现（2026-10-24） |
| [loop-and-progress-fixes.md](loop-and-progress-fixes.md) | 成功改过文件后重跑同一条命令不算死循环；进度文件加 `fix_check` 改写错的校验命令（计划 7.2、7.5） | 已实现（2026-10-24） |
| [effort-picker.md](effort-picker.md) | 网页端选推理强度，每轮可以换，默认在设置里（计划 3.5） | 已实现（2026-10-24） |
| [notify-unread.md](notify-unread.md) | 列表标出等审批、等回答、有新结果的对话；浏览器通知（计划 3.6、3.7） | 已实现（2026-10-24） |
| [edit-resend.md](edit-resend.md) | 每一轮的问题能“编辑”：撤销这一轮及之后再用改过的问题重新发（计划 3.2） | 已实现（2026-10-24） |
| [workspace-diff.md](workspace-diff.md) | “改动”面板看工作区相对 HEAD 的 diff，每轮末尾列出改过的文件（计划 3.3） | 已实现（2026-10-24） |
| [at-mention.md](at-mention.md) | 输入框里 `@` 模糊搜索工作区文件并插入路径（计划 3.4） | 已实现（2026-10-24） |
| [queued-followups.md](queued-followups.md) | 排队的后续消息：Tab 排到这一轮结束后，队列在服务端（计划 3.1） | 已实现（2026-10-24） |
| [thread-search.md](thread-search.md) | 搜索对话的名字、问题和回答（计划 4.2） | 已实现（2026-10-24） |
| [workspace-more.md](workspace-more.md) | 工作区候选目录、按工作区筛对话、工作区改名、登记不是 git 仓库的目录（计划 1.3–1.6） | 已实现（2026-10-24） |
| [remote-setup.md](remote-setup.md) | 远程主机：把本机的 tern 装到远端、连不上时说清是哪一步、网页里确认指纹、把本机密钥复制到远端（计划 2.1–2.4） | 设计（2026-10-24），交给 tern 实现 |
| [review-scope.md](review-scope.md) | `/review` 可以指定范围：未提交的改动、对比某个分支、某个提交（计划 3.13） | 设计（2026-10-24），交给 tern 实现 |
| [web-esc-copy-export.md](web-esc-copy-export.md) | 网页端 Esc 打断、复制回答、导出对话为 Markdown（计划 3.8、3.9） | 设计（2026-10-24），交给 tern 实现 |
| [side-question.md](side-question.md) | 旁支提问：临时分出去问一句，不进原对话（计划 3.10） | 设计（2026-10-24），交给 tern 实现 |
| [thread-goal.md](thread-goal.md) | 对话的目标：常显在标题下面，改了下一轮告诉模型（计划 4.3） | 设计（2026-10-24），交给 tern 实现 |
| [copy-workspace.md](copy-workspace.md) | 在工作区的副本里开对话，做完“应用到”原目录（计划 1.7） | 设计（2026-10-24），交给 tern 实现 |
| [shell-command.md](shell-command.md) | 网页端 `!命令`：在这条对话的沙箱里跑一条命令，下一轮模型看得到（计划 3.11） | 设计（2026-10-24），交给 tern 实现 |
| [background-jobs-panel.md](background-jobs-panel.md) | 网页端看、停模型起的后台命令（计划 3.12） | 设计（2026-10-24），交给 tern 实现 |
| [bash-interactive.md](bash-interactive.md) | 后台命令能写输入（`bash_input`），可选伪终端（计划 6.2） | 设计（2026-10-24），交给 tern 实现 |
| [project-config.md](project-config.md) | 项目级配置：工作区里的 `.agents/tern.json`（计划 5.2） | 设计（2026-10-24），交给 tern 实现 |
| [mcp-resources.md](mcp-resources.md) | MCP 资源：`list_mcp_resources`、`read_mcp_resource`（计划 6.3） | 设计（2026-10-24），交给 tern 实现 |
| [mcp-oauth.md](mcp-oauth.md) | MCP 的 OAuth 登录，令牌存 `.data/mcp-tokens.json`（计划 6.4） | 设计（2026-10-24），交给 tern 实现 |
| [tern-mcp-server.md](tern-mcp-server.md) | `tern mcp-server`：别的客户端把 tern 当 MCP 工具用（计划 6.5） | 设计（2026-10-24），交给 tern 实现 |
| [macos-sandbox.md](macos-sandbox.md) | macOS 上的 bash 沙箱（Seatbelt）（计划 6.1） | 设计（2026-10-24），交给 tern 实现 |
| [architecture.md](architecture.md) | 现状：进程、模块与数据链路（界面 → app-server → agent → 模型与工具），数据落在哪 | 说明（2026-10-23），不是设计 |
| [remote-agent-design.md](remote-agent-design.md)（配图页面 [remote-agent.html](remote-agent.html)） | 远程 agent：网页上的对话交给用户本机的 tern 执行；tern 的几种界面与命名 | 搁置（2026-10-23），改按 app-server-design.md 做。前置改动“macOS 上能编译”已完成 |
| [remote-control-research.html](remote-control-research.html) | 调研：Claude Code 的 Remote Control、Codex 的远程连接与 app-server，和 tern 远程 agent 的两种方案对比，以及要先对齐的产品问题 | 调研（2026-10-23），不是设计 |
| [bash-timeout-and-polling.md](bash-timeout-and-polling.md) | bash 单条命令时限 2 分钟；轮询后台任务不算重复动作 | 已实现（2026-10-23） |
| [sandbox-memory-and-defaults.md](sandbox-memory-and-defaults.md) | 沙箱内存 2G 与 OOM 说明；命令行默认值和网页端一致；代理地址改用 `AGENT_PROXY_URL` | 已实现（2026-10-23） |

## 还没写设计的待办

- `edit_file` 匹配失败时给出文件里最接近的原文。2026-10-23 实现第一份 spec 时，tern 有 4 次编辑因为 gofmt 对齐过的空白没对上而失败，每次都要多花几轮去看原文。
- 只读模块缓存引起的 `go: writing stat cache … read-only file system` 提示：不影响结果，但模型每次都要解释一遍。
- tern 的沙箱里连不到模型接口，需要真实模型的验收只能在沙箱外由人或 Claude Code 跑。
- 知识库资料 `retrieval/corpus.md` 讲的还是 learning-agent，里面仍写“观测台”。
- 重复动作检测会误判“改测试 → 重编 → 再跑同一条命令”：命令文本相同，中间改过文件也算重复。2026-10-24 实现 app-server 第 3 块时，tern 在 149 步被拦下（6 步里同一条 bash 出现 3 次），工作已做完、只差最后的验证。
- `.env` 里验证账号的变量名还叫 `OBSERVER_TEST_USER`、`OBSERVER_TEST_PASSWORD`；改名要同时改用户本机的 `.env`。
- （已修，见 sandbox-quiet-chrome.md）tern 在沙箱里用无头 Chrome 测页面时，Chrome 自己的后台流量（update.googleapis.com、safebrowsing.googleapis.com 等十来个域名）每个都弹一次联网审批。2026-10-23 实现 app-server 第二期时两块都碰到，只能逐个拒绝。
- 删除对话时子 agent 自己的检查点不在 `run.turnIDs()` 里，会留下来（只占磁盘，不出错）。2026-10-24 review conversation-manage.md 时发现。
- 宿主机上 `go env GOMODCACHE` 指的目录不存在时（没下载过任何模块），bash 沙箱整个起不来：`bwrap: Can't find source path …/go/pkg/mod`。应该只是不挂模块缓存。2026-10-24 验收内置命令时，把 `HOME` 指到临时目录碰到的。
- 沙箱把工作区里已有的 `.data/` 换成空的 tmpfs，tern 在沙箱里 `rm -rf .data` 删不到真的：2026-10-24 实现 tern-home-default.md 时，它报告“验收留下的 `.data/` 已经删掉”，其实还在，挡住了后面一条验收。
- tern 的进度文件里写错的校验命令改不了：第二期第 2 块的一条校验命令有笔误（要求代码里出现一个不可能有的字面量），进度文件只能由工具改，tern 自己修不了，那一项一直是“未通过”。
