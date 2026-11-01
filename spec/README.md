# 设计文档

新功能先在这里写设计，确认后交给 tern 实现（分工见 [AGENTS.md](../AGENTS.md)，交给 tern 的步骤见 [.claude/skills/tern-implement](../.claude/skills/tern-implement/SKILL.md)）。一份设计一个 Markdown 文件，结构参照已有的几份：为什么要改、改什么、按文件的改动（含不要改的）、这一期不做什么、验收。只有用户要求时才另配 HTML 页面，两者内容一致，有出入以 Markdown 为准。

状态核对日期：2026-10-25。本轮代码已合入本地 `main`（`f2e4296`）。[Codex 对齐清单](codex-parity-plan.md)第 1–7 节的 40 个编号项已完成，第 8 节的 11 项终端体验仍暂缓；app-server 第三期桌面端尚未开发。实现完成不等于覆盖所有部署环境，验收证据与范围见 [收尾记录](closeout-20261025.md)。

下面的索引记录各份设计当前状态；文档正文中的问题背景、分期和验收步骤保留历史语境。未完成事项、已修复旧待办和现有限制分别列在索引之后。

| 文档 | 内容 | 状态 |
| --- | --- | --- |
| [harness-next-phase.md](harness-next-phase.md) | **下一阶段迭代计划**：先稳住刚合并的网页功能，主线是 agent 质量的度量与回归（扩充编码评测、合并前回归门槛、按失败归因改进），并行收口网页产品、清旧账；流程回到“spec → tern 实现 → review”，小批次合并 | 计划（2026-10-29），用户已定：主线第 1 期，tern 实现，编码评测用 DeepSeek、通用评测用豆包 |
| [web-polish-20261031.md](web-polish-20261031.md) | **网页端打磨**：工具行写成人话、每轮改动卡片与计划卡片、短标题、空态居中、侧栏去重、改动与计划面板去掉重复头部、审批卡片精简、图标不依赖字形、设置页不挂输入框、默认用配好的模型 | 两批都已实现并验收（2026-10-31，Claude Code 直接实现） |
| [web-preview-p1.md](web-preview-p1.md) | **网页预览 P1**（EXT-06 第一段）：右侧工作区打开本机开发服务，能点、能输入、能滚动；Chrome 保留自己的沙箱，网络只经一个只放行授权 origin 的代理 | 已实现并验收（2026-10-31），macOS 未实测 |
| [eval-cost-visibility.md](eval-cost-visibility.md) | **评测的成本与耗时**：逐题请求次数、未命中 tokens、耗时，和基线配对的成本差；网页端换基线 | 已实现并验收（2026-10-31） |
| [cleanup-phase3.md](cleanup-phase3.md) | **第 3 期清旧账**：删对话连子 agent 检查点一起删、沙箱 Go 的 stat cache 警告、知识库旧名称 | 已实现并验收（2026-10-31） |
| [queue-restart-version.md](queue-restart-version.md) | **队列版本**：已暂停的队列，app-server 重启不再改版本 | 已实现并验收（2026-10-31） |
| [final-answer-reserve.md](final-answer-reserve.md) | **最后一次请求留给最终回答**：设了 `max_steps` 时最后一次请求禁止调用工具，逼出最终回答；75% 的预算提醒改成逐项核对。依据是 flash 基线和回放实验 | 已实现（2026-10-31）；三种协议的 `-max-steps 3` 验收通过，整题评测见 harness-next-phase.md 进度 |
| [web-qa-fixes-20261030.md](web-qa-fixes-20261030.md) | **网页端点验修复**：浏览器逐项点验的结果；计划面板这一轮结束后批准按钮不可点（P1）、提交说明不清空、发送回执提示不消失 | 已实现并验收（2026-10-31，tern 实现）；第二批（第 7 节）：沙箱里关掉 Go 遥测、任务产物默认收起隐藏项，也已验收 |
| [request-budget-reminder.md](request-budget-reminder.md) | **请求预算提醒**：设了 `max_steps` 时，用掉 50%、75% 各插一条提醒（用了多少、用完直接停、只做交给你的部分）；来自 `spec-thread-goal` 越界验收的回放证据 | 已实现并验收（2026-10-29，tern 实现）；编码评测 5 道 spec 题通过 11/15 → 15/15 |
| [web-product/](web-product/README.md) | **网页产品方案**：Codex 体验对齐，含界面、交互、子 agent、9 类日常工作流补充；共 47 项需求、42 个验收场景，已有能力与新增体验分开记录 | 开发中（2026-10-25），进度见 web-product/08-implementation.md；不计入旧功能完成数 |
| [codex-parity-plan.md](codex-parity-plan.md) | **任务清单**：和 Codex 对齐的功能点，哪些没做、哪些做得不好，按优先级排；做某一条前先写它的 spec，填回这里 | 清单（2026-10-24），持续更新 |
| [app-server-design.md](app-server-design.md) | 照 Codex 的 SSH 远程：常驻的 `tern app-server` 管对话，网页端、终端、以后的桌面端经 Unix socket 或 SSH 连它；`TERN_HOME` | 第一步（`TERN_HOME`、`-agent-server`）已实现（2026-10-23）；第一期（app-server、网页端与 chat 改成客户端、去掉租户账号、SSH 远程与重连）已实现（2026-10-24）。第二期另写在 app-server-phase2-design.md，已实现（2026-10-23）；第三期桌面端未做 |
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
| [remote-setup.md](remote-setup.md) | 远程主机：把本机的 tern 装到远端、连不上时说清是哪一步、网页里确认指纹、把本机密钥复制到远端（计划 2.1–2.4） | 已实现并收尾验证（2026-10-25），见收尾记录 |
| [review-scope.md](review-scope.md) | `/review` 可以指定范围：未提交的改动、对比某个分支、某个提交（计划 3.13） | 已实现并收尾验证（2026-10-25），见收尾记录 |
| [web-esc-copy-export.md](web-esc-copy-export.md) | 网页端 Esc 打断、复制回答、导出对话为 Markdown（计划 3.8、3.9） | 已实现并收尾验证（2026-10-25），见收尾记录 |
| [side-question.md](side-question.md) | 旁支提问：临时分出去问一句，不进原对话（计划 3.10） | 已实现并收尾验证（2026-10-25），见收尾记录 |
| [thread-goal.md](thread-goal.md) | 对话的目标：常显在标题下面，改了下一轮告诉模型（计划 4.3） | 已实现并收尾验证（2026-10-25），见收尾记录 |
| [copy-workspace.md](copy-workspace.md) | 在工作区的副本里开对话，做完“应用到”原目录（计划 1.7） | 已实现并收尾验证（2026-10-25），见收尾记录 |
| [shell-command.md](shell-command.md) | 网页端 `!命令`：在这条对话的沙箱里跑一条命令，下一轮模型看得到（计划 3.11） | 已实现并收尾验证（2026-10-25），见收尾记录 |
| [background-jobs-panel.md](background-jobs-panel.md) | 网页端看、停模型起的后台命令（计划 3.12） | 已实现并收尾验证（2026-10-25），见收尾记录 |
| [bash-interactive.md](bash-interactive.md) | 后台命令能写输入（`bash_input`），可选伪终端（计划 6.2） | 已实现并收尾验证（2026-10-25），见收尾记录 |
| [project-config.md](project-config.md) | 项目级配置：工作区里的 `.agents/tern.json`（计划 5.2） | 已实现并收尾验证（2026-10-25），见收尾记录 |
| [mcp-resources.md](mcp-resources.md) | MCP 资源：`list_mcp_resources`、`read_mcp_resource`（计划 6.3） | 已实现并收尾验证（2026-10-25），见收尾记录 |
| [mcp-oauth.md](mcp-oauth.md) | MCP 的 OAuth 登录，令牌存 `.data/mcp-tokens.json`（计划 6.4） | 已实现并通过 Keycloak 真实授权验收（2026-10-25），见收尾记录 |
| [tern-mcp-server.md](tern-mcp-server.md) | `tern mcp-server`：别的客户端把 tern 当 MCP 工具用（计划 6.5） | 已实现并收尾验证（2026-10-25），见收尾记录 |
| [macos-sandbox.md](macos-sandbox.md) | macOS 上的 bash 沙箱（Seatbelt）（计划 6.1） | 已实现并收尾验证（2026-10-25），见收尾记录 |
| [sandbox-modes.md](sandbox-modes.md) | 沙箱与审批：对外三档（请求批准 / 帮我批准 / 完全访问），内部 `permission` + `sandbox` 两个旋钮；新增 `-bash-host` 与 `sandbox_mode`，「完全访问」在宿主机执行；只读计划与无人值守走高级项；项目配置不能开 | 已实现（2026-10-26）；2026-10-29 合并前用真实模型验证了宿主机执行、`!命令`、审计与子 agent 继承，见 web-product/08-implementation.md 末节 |
| [architecture.md](architecture.md) | 现状：进程、模块与数据链路（界面 → app-server → agent → 模型与工具），数据落在哪 | 说明（2026-10-23），不是设计 |
| [remote-agent-design.md](remote-agent-design.md)（配图页面 [remote-agent.html](remote-agent.html)） | 远程 agent：网页上的对话交给用户本机的 tern 执行；tern 的几种界面与命名 | 已被 app-server-design.md 替代，不再排期；保留历史方案。前置改动“macOS 上能编译”已完成 |
| [remote-control-research.html](remote-control-research.html) | 调研：Claude Code 的 Remote Control、Codex 的远程连接与 app-server，和 tern 远程 agent 的两种方案对比，以及要先对齐的产品问题 | 调研（2026-10-23），不是设计 |
| [bash-timeout-and-polling.md](bash-timeout-and-polling.md) | bash 单条命令时限 2 分钟；轮询后台任务不算重复动作 | 已实现（2026-10-23） |
| [sandbox-memory-and-defaults.md](sandbox-memory-and-defaults.md) | 沙箱内存 2G 与 OOM 说明；命令行默认值和网页端一致；代理地址改用 `AGENT_PROXY_URL` | 已实现（2026-10-23） |

## 未完成与暂缓

这张表只列尚未关闭的工作，不把已经完成的旧问题继续当待办。历史问题未重新复现的明确标为“待复测”，不推断它一定仍能触发。

| 事项 | 状态 | 范围与下一步 |
| --- | --- | --- |
| 终端界面与 CLI 体验（11 项） | 暂缓 | 包括输入编辑、历史、多行、状态栏、终端斜杠命令、渲染、续聊选择、机器可读输出、配置和补全；完整清单见 [计划第 8 节](codex-parity-plan.md#8-暂缓命令行界面)。没有重新启动这批开发 |
| app-server 第三期：桌面端 | 未开发 | 见 [app-server 分期](app-server-design.md#11-分期)；现有网页端、多主机与 SSH 已实现 |
| `tern web` 从源码编译并自动替换占用端口的旧网页端 | 方案讨论，未排期 | 本轮会话提出，尚无独立 spec 或实现；现有 [app-server 自动替换](app-server-auto-replace.md)只处理后端，不监听源码，也不替换旧网页端进程 |
| Go 只读模块缓存的 stat cache 警告 | 2026-10-29 复现，待处理（编码评测的校验命令在沙箱里 `go test`、`go build` 时出现） | `go: writing stat cache … read-only file system` 曾不影响结果但反复产生解释。当前模块缓存仍只读挂载；“缓存目录不存在导致启动失败”已另行修复，不代表这条警告也已解决 |
| 知识库中的旧名称和过时说明 | 未整理 | `retrieval/corpus.md` 仍有“观测台”，检索提示词仍提 learning-agent；需一起核对资料与提示词，不只改标题 |
| 删除对话后的子 agent 检查点清理 | 历史问题，待专项回归 | 原记录指出子 agent 的检查点可能不在 `run.turnIDs()` 的删除范围。当前删除仍按轮次列表清理；需验证子任务和分叉共享场景，尚无这项的关闭证据 |

## 已修复的旧待办

以下五项均已有对应实现和验收记录，移出待办；原始故障背景保留在各自 spec 中。

| 原问题 | 当前状态 | 实现与验收依据 |
| --- | --- | --- |
| `edit_file` 匹配失败只报错，不提供接近原文 | 已修复 | [edit-file-lenient.md](edit-file-lenient.md)；`internal/workspace/lenient.go` 的分级空白匹配与 `closest`/`notFound` 提示 |
| 改文件后重跑同一命令被当作死循环 | 已修复（文件工具） | [loop-and-progress-fixes.md](loop-and-progress-fixes.md)；成功的 `edit_file`/`write_file` 清空动作窗口。通过 bash 修改文件不在本期范围 |
| Chrome 后台请求逐个弹联网审批 | 已修复 | [sandbox-quiet-chrome.md](sandbox-quiet-chrome.md)；Chrome 包装参数、PAC 和后台域名拒绝规则 |
| Go 模块缓存目录不存在导致整个沙箱无法启动 | 已修复 | [sandbox-quiet-chrome.md](sandbox-quiet-chrome.md)；`internal/sandbox/launch_linux.go` 对模块缓存采用 `--ro-bind-try` |
| 进度文件里写错的校验命令无法更正 | 已修复 | [loop-and-progress-fixes.md](loop-and-progress-fixes.md)；`progress(action=fix_check)` 可修改未通过项，要求原因并重新验证 |

## 现有限制与验收边界

- **沙箱内直接调用模型接口：** 未提供让沙箱中的验收程序自动获得真实模型凭据和网络访问的机制。需要真实模型的验收仍在隔离的沙箱外测试环境执行；后续如要内置这类能力，先设计凭据与网络边界。
- **沙箱里的 `.data` 不等于真实目录：** 工作区已有的 `.data`/`.cache` 会被 tmpfs 遮蔽。这是现有隔离行为；在沙箱里执行 `rm -rf .data` 不能作为真实目录已清理的证据。验证流程应在沙箱外检查和清理自己的临时数据，不能通过取消隔离来“修复”。
- **SSH、Mac、OAuth：** 已有真实 OpenSSH、Mac 真机和 Keycloak 授权验收，环境与覆盖范围见 [收尾记录](closeout-20261025.md)。不代表所有跳板机、异构远端或第三方 OAuth 服务均已验证。个人 Claude Code/Codex 的持久 MCP 配置未改动，不属于本轮代码缺口。
- **旧方案：** [remote-agent-design.md](remote-agent-design.md) 已由 app-server＋SSH 方案替代，不再列入待开发任务；调研与架构说明也不计入功能完成数。
