# 依据与核对边界

核对日期：2026-10-25。代码基线：`6fba4ad6d8094fb837c60dc1cfa24a2d1d778119`。

此文保存产品结论的来源，不保存私人对话正文、账户配置或密钥。表中的行号基于该提交，开发后应按函数名定位。

## 本次实际检查

- 阅读根、web、app-server、protocol 的 AGENTS 及现有对齐清单、工作区/副本等相关 spec。
- 检查前端布局、输入、对话、面板、配置、连接与协议方法表的真实代码。
- 只读观察正在运行的 8090 页面：侧栏、标题、当前对话、输入控件；打开改动面板，看到工作区基准 `6fba4ad`，随后关闭恢复布局。
- 当时 1128×900 视口中，左侧管理入口较多，工作区以筛选下拉呈现，模型/工作区在已有对话里禁用；打开右栏后对话与输入明显收窄。这是观察事实；“应重排入口”是本方案的产品判断。
- 浏览器中的历史 assistant 回答不是当前实现证据。本次没有据旧回答里的“未实现”判断代码状态。
- 读取 Codex 官方文档核对核心产品语义，没有用旧的 app-server 协议存在某方法推断完整 UI 已对齐。

没有在本轮运行真实模型、创建任务、修改设置、停止服务、提交/推送代码，也没有做完整 UI 回归。正在运行的 web 可执行文件版本未重新核验；页面观察只证明看到的呈现，源码事实以固定提交为准。

## 当前实现定位

以下路径从本文件所在目录可直接访问。

| 编号 | 文件与位置 | 支持的结论 |
| --- | --- | --- |
| E01 | [index.html](../../internal/web/page/index.html)，19–107；[app.css](../../internal/web/page/app.css) | 管理导向侧栏、顶部按钮、输入选择器、共享 subpanel；布局基础 |
| E02 | [shell.js](../../internal/web/page/shell.js)，297 `loadRuns`、412 `groupRows`、453 `runMenu`、614 `searchHost`；[workspace-more.md](../workspace-more.md) 2.2/2.3 | 主机分组、按名字筛选、改名/归档/删除/搜索；项目树与稳定历史归属不是已有能力 |
| E03 | [shell.js](../../internal/web/page/shell.js)，176 `syncComposer` | 模型/工作区固定、权限/effort 逐轮选择、停止状态、图片模型限制 |
| E04 | [composer.js](../../internal/web/page/composer.js)，5 `start`、47 `addImages`、106 `runShell`、129 `steer`、174 `queueSection`、292 键盘处理、340 `syncAt` | 图片、命令、插话与队列的具体入口；队列没有编辑排序；发送状态和快捷键 |
| E05 | [chat.js](../../internal/web/page/chat.js)，152 `changedLine`、192 `exploreGroup`、206 `renderCall`、380 `renderChat`、521 滚动逻辑 | 活动折叠、工具/原始 I/O 展示、轮次操作、流式跟随 |
| E06 | [sandbox.js](../../internal/web/page/sandbox.js)，82 `approvalCard`、110 `askCard`；[shell.js](../../internal/web/page/shell.js)，379 `notifyChanges` | 已有审批/提问卡与通知，不应列为“没有审批” |
| E07 | [diff.js](../../internal/web/page/diff.js)，7 `loadDiff`、20 `openDiff`、30 `diffBody`、43 `renderDiffPanel`；[files.js](../../internal/web/page/files.js)，21 `previewFile`、56 `renderFilesPanel` | 固定范围只读 diff；沙箱文件预览不是项目文件浏览器；现有预览大小/类型限制 |
| E08 | [util.js](../../internal/web/page/util.js)，23 `ui`、37 本地存储 helper、41 未读状态；[composer.js](../../internal/web/page/composer.js)，27 `attached` | 主输入未见按任务草稿持久化；steer/附件有页面内状态；未读为浏览器本地 |
| E09 | [connection.js](../../internal/web/page/connection.js)，11 `watchConnection`、104 `fillRemote`；[web AGENTS](../../internal/web/AGENTS.md) | 多主机、版本/安装、SSH 错误与恢复现有边界；换主机整页导航 |
| E10 | [handle.go](../../internal/appserver/handle.go)，104–136 方法表；[app-server AGENTS](../../internal/appserver/AGENTS.md) | 已有 thread/turn/queue/approval/workspace 方法；元数据、审批和队列事实源 |
| E11 | [copy-workspace.md](../copy-workspace.md) 2；[app-server AGENTS](../../internal/appserver/AGENTS.md) 分叉与副本；[workspace-more.md](../workspace-more.md) | clone、副本基线、应用回去、分叉共享目录；工作区按名字的历史限制 |
| E12 | [follow.js](../../internal/web/page/follow.js)，28 `show` 及其 `es.onerror` | 浏览器 SSE 出错后 close 并提示重新选择；和 appclient 自动重连不是同一层 |
| E13 | [agents.js](../../internal/web/page/agents.js)，51 `renderSubPanel`；[jobs.js](../../internal/web/page/jobs.js)、[browser.js](../../internal/web/page/browser.js) | 面板互斥与现有命令/浏览器/子任务视图 |
| E14 | [review-scope.md](../review-scope.md)、[workspace-diff.md](../workspace-diff.md)；[diff.go](../../internal/appserver/diff.go) | `/review` 范围和 diff 面板范围是两件事；不能互相当完成证据 |
| E15 | [shell-command.md](../shell-command.md)、[background-jobs-panel.md](../background-jobs-panel.md)、[bash-interactive.md](../bash-interactive.md) | `!`、后台任务、agent PTY 的边界，不等同浏览器交互终端 |
| E16 | [pages.js](../../internal/web/page/pages.js)，90 `openSettings`、285 `pickWorkspace`、407 `openSkills`、458 `openMCP` | 设置长页、配置继承、目录浏览、工具中心、MCP 授权既有能力 |
| E17 | [app-server-auto-replace.md](../app-server-auto-replace.md)、[现有未完成清单](../README.md#未完成与暂缓) | 后端替换已存在；源码编译并替换 web 尚未实现 |
| E18 | [app.css](../../internal/web/page/app.css)，1 色彩 token、12 系统暗色、28 焦点样式；响应式规则 | 主题和基础可访问性已存在，目标是增强；不能写“完全没有暗色” |

“未见”表示在对应模块和方法表里未发现，不是对整个仓库全部未来可能路径的形式化证明。实施前仍需核对最新代码，避免已被其他提交补齐的能力重复开发。

## 子 agent 补充核对

2026-10-25 用户反馈后追加阅读以下代码。仅静态核对，没有在本轮派发真实子任务，不能将待复现问题写成已完成验收。

| 编号 | 文件与位置 | 支持的结论 |
| --- | --- | --- |
| E19 | [agents.js](../../internal/web/page/agents.js)，10 `subList`、17 `subState`、29 `agentCard`、51 `renderSubPanel`；[runs.js](../../internal/web/page/runs.js)，171–176 子任务事件处理 | 当前按调用列卡、状态优先看外层工具成功、固定“看不到父对话”文案、过程侧栏；没有用户直接发送/停止控件 |
| E20 | [subcontrol.go](../../internal/agent/subcontrol.go)，17 控制面说明、49 `forkHistory`、67 `startBackground`、103 `outcome`、173 `agentControl`、231 `stopBackground`；[subagent.go](../../internal/agent/subagent.go)，81 `spawnAgent`、131 `runSub`；[agent AGENTS](../../internal/agent/AGENTS.md) | 后台 spawn 返回 running，已有 wait/send/stop/fork、按 task_id 续跑与结果回放；后台子任务跟随父轮生命周期。父 agent 工具能力不能直接等同网页控制 API |

交叉核对发现：`subState` 先判外层 `res.status === "ok"` 就返回 done，但 `startBackground` 的成功返回只表示子任务已启动；前端详情统一写新上下文，但后端支持 fork 继承。两者作为 SUB-02、SUB-07 的优先核验路径。

## Codex 官方语义参考

以下均在本轮实际读取。文档会变化，标明 surface=桌面编码体验，不把 ChatGPT Work、CLI、云执行的全部能力混作一个首版目标。只摘取与本方案有关的语义，其余产品规格是 Tern 自己的建议。

| 编号 | 来源 | 本方案使用范围 |
| --- | --- | --- |
| O01 | [Projects and chats](https://learn.chatgpt.com/docs/projects)（通过官方 projects 路径读取） | 项目组织任务、置顶/归档、多目录与远程边界；首版 Tern 只保留单目录项目 |
| O02 | [Code review](https://learn.chatgpt.com/docs/code-review)（通过官方 app/review 路径读取） | 本地 diff 范围、行反馈、文件/hunk 操作；评审与实际修改/提交分开 |
| O03 | [Prompting](https://learn.chatgpt.com/docs/prompting) | steer 与 queue 的区别；桌面可配置 follow-up 行为；不能直接把 CLI 的 Tab 当作网页标准 |
| O04 | [Worktrees](https://learn.chatgpt.com/docs/environments/git-worktrees) | worktree 和 Handoff 是目录/Git 生命周期，不能把 Tern clone 改名就当实现 |
| O05 | [Commands](https://learn.chatgpt.com/docs/reference/commands) | 命令菜单、当前对话查找、输入恢复、文件导航、多标签与布局；网页快捷键需适配 |
| O06 | [Notifications](https://learn.chatgpt.com/docs/notifications) | Activity、未读与运行/待处理聚合、通知分类；不等同网页关闭后的推送能力 |
| O07 | [Slash commands](https://learn.chatgpt.com/docs/reference/slash-commands) | 显式 Skill、compact/status/usage 和 Goal 入口；按当前账户/环境可用性理解 |
| O08 | [Local environments](https://learn.chatgpt.com/docs/environments/local-environment) | 项目常用动作、环境初始化；Tern 使用当前 clone 边界而非假装真 worktree |
| O09 | [Browser](https://learn.chatgpt.com/docs/browser) | 开发页面预览、元素/区域批注、发送反馈后修改复验；不推断任意站点都可嵌入 Tern Web |
| O10 | [Work with files](https://learn.chatgpt.com/docs/artifacts-viewer) | 产物预览、源码/渲染与定位批注；具体 Tern 格式范围在 EXT-07 单独定义 |
| O11 | [Long-running work](https://learn.chatgpt.com/docs/long-running-work) | 持续目标、暂停/恢复/修改、完成标准；Tern 的调度/预算契约为新增设计 |

O05–O11 已在本次补漏核查的上一轮实际读取，本轮据此写入 EXT-01–09，不重复抓取。对应 Tern 基础已检查 `shell.js` 的 `updateCtxRing`、`chat.js` 的 `renderCompact` 与能力卡、`composer.js` 的补全、`browser.js` 的过程面板、`md.js` 的可视化块渲染。新增工作流不等于这些基础能力不存在；具体新能力仍需实现与验收。

官方入口 [Features](https://developers.openai.com/codex/app/features) 当前包含多种工作能力；本方案主动限制为编码任务核心流程，不把该页每一项变成必须复刻的需求。

## 待验证，不能当作结论

- 尚未固定用户期望对齐的 Codex 桌面版本与完整界面截图，因此不承诺精确菜单名称、像素、图标、动画一致。
- 首次登录、加载超大历史、移动设备键盘和多页面并发未在本轮实测；相应需求来自代码分析和产品健壮性要求，验收列为待执行。
- 模型每轮切换、完整终端、精确最后一轮 diff、真实 worktree/Handoff 需要独立后端设计；本轮没有做可行性实现证明。
- 当前工作区重命名、草稿、SSE 的代码限制是发现依据，不把尚未实际复现的数据丢失或重复执行描述成已确认事故。

## 本次文档交付检查

2026-10-25 补漏收录核对：再次读取 [Browser 官方说明](https://learn.chatgpt.com/docs/browser) 的桌面端预览与批注部分，确认 EXT-06 所参考的“预览 → 批注 → 发送 → 修改复验”语义。Tern 的控制通道、VM 端口可达性和状态持久化仍是自身待实现契约，不能据官方参照认定已实现；本次未重新验证 Tern 运行行为，也未变更 08 的实现状态。

文档应通过：本地 Markdown 链接目标存在、需求 ID 唯一、批次和需求互相可追踪、无密钥/私有对话正文、diff 无空白错误。只做文档时不以构建或模型验证替代上述检查；最终实际检查结果在交付消息中说明。
