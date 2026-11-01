# 实现与验收记录

状态：开发中。目标为 web-product 的完整功能范围；不得以单批交付代替整个目标。当前主线为 [18-codex-visual-alignment.md](18-codex-visual-alignment.md)：用户要求页面风格、布局和操作点尽量参照 Codex，功能通过不等于视觉验收通过。

用户于 2026-10-25 授权开始持续开发。工作分支：`feat/web-product`，基线 `6fba4ad`。尚未提交。日常 8090 服务与 `~/.tern` 不用于变更性验收；验证在隔离 clone 和 ext4 运行目录进行。

## 实现约定

按 04 的 B1–B5 及补充排期推进；当前明确未开始的后续能力继续保留，不删除要求。每项只有对应完整验收通过才可标“已验收”。实现/静态检查/真实运行分开记。用户的开发授权已给出，不再逐条索要开工确认；提交前仍按仓库要求交付具体 review 结果。

首批文件与行为：

- `internal/web/page/agents.js`、新增 `substate.js`：按稳定子任务聚合尝试，修正后台启动回执被误判完成；过程、结果、上下文模式分开显示。
- `internal/agent/subagent.go`、`subcontrol.go`、`internal/web/page/runs.js`：补充上下文模式与结果元数据，兼容缺少字段的历史；不改权限或后台生命周期。
- `internal/web/page/index.html`、`app.css`、`shell.js`、`main.js`、新增 `navigation.js`：重排日常导航，管理入口下沉；保留旧入口行为。后续持续补足项目身份和命令面板。
- 本批不通过前端模拟队列、Goal 调度或子任务直控；这些按后续实现契约落在服务端。

验证：JS 语法、Go 格式/vet/build、真实隔离子任务与页面；后台 spawn 仍运行时不得显示完成、fork 显示继承模式、结束结果可追溯。完整 A01–A42 尚待逐项执行。

## 需求台账

2026-10-31 按实际证据重标（[下一阶段计划](../harness-next-phase.md) 2.5）：**已验收（脚本）**＝`spec/web-product/run-checks.py` 一条命令跑的脚本覆盖、2026-10-31 通过（整套 31/32，唯一失败是队列版本的 bug，修复后该项通过）；**已验收（浏览器）**＝2026-10-30/31 Claude Code 用无头 Chrome 逐项点验通过，没有脚本；**部分验收**＝写明了没覆盖的部分；**保留/补强**照旧是沿用旧能力。不再用“定向验收”。

| ID | 需求 | 状态 | 实现与证据 |
| --- | --- | --- | --- |
| NAV-01 | [打开网页就能找到手头任务](01-current-and-gaps.md) | 已验收（脚本） | run-checks：navigation；原记录：项目导航、首屏空态与项目新任务入口；导航批次 |
| NAV-02 | [一个项目下管理多条任务](01-current-and-gaps.md) | 部分验收 | run-checks：navigation 通过；两台真实主机的组合未验；原记录：稳定目录身份、改名与历史归属；两真实主机组合尚未复验 |
| NAV-03 | [长期保留常用任务](01-current-and-gaps.md) | 已验收（脚本） | run-checks：navigation（置顶、归档/恢复）；原记录：个人置顶、归档/恢复、全文搜索；导航/草稿批次 |
| NAV-04 | [快速找回旧讨论](01-current-and-gaps.md) | 已验收（浏览器） | 10-30 无头 Chrome 点验：命令面板与搜索；原记录：服务端全文搜索、命中片段和任务内分页查找；20及closeout报告 |
| CTX-01 | [发送前确认会在哪里改文件](01-current-and-gaps.md) | 已验收（脚本） | run-checks：navigation、copy-delivery；原记录：主机/实际目录/副本信息；导航批次及copy-delivery回归 |
| CTX-02 | [知道用什么模型，必要时切换](01-current-and-gaps.md) | 已验收（浏览器） | 10-30 点验：模型与推理下拉、按对话选模型；原记录：模型与推理菜单；历史模型固定并解释限制 |
| CTX-03 | [给 agent 提供必要上下文](01-current-and-gaps.md) | 保留/补强；部分验收 | 图片、@文件、显式Skill和产物上传入口；一般文件无自动解析承诺 |
| CHAT-01 | [快速了解 agent 做到哪一步](01-current-and-gaps.md) | 已验收（脚本） | run-checks：goal-plan、subcontrol；原记录：当前阶段、工具过程与最终结果；真实Goal/子任务页面 |
| CHAT-02 | [工作中补充要求或安排下一件事](01-current-and-gaps.md) | 已验收（脚本） | run-checks：managed-steer 8 个场景、queue start/finish（2026-10-31 修了重启改队列版本的 bug）；原记录：持久插话、队列排序/恢复；managed-steer和仲裁报告 |
| CHAT-03 | [切任务、刷新、断线后继续写](01-current-and-gaps.md) | 部分验收 | run-checks：delivery（发送回执、去重）；草稿冲突合并只有 10-25 批次的定向证据；原记录：账号/主机/任务草稿、冲突合并和请求回执；草稿批次 |
| CHAT-04 | [一眼发现等待自己的事项](01-current-and-gaps.md) | 部分验收 | 10-30 点验：审批卡片与活动中心；check-activity、check-subinput 要人在网页上答，没在一条命令里；原记录：活动中心和真实待处理来源；activity/subinput报告 |
| CHAT-05 | [停下或继续工作](01-current-and-gaps.md) | 已验收（脚本） | run-checks：queue、goal-plan pause、managed-steer stop；原记录：停止、队列暂停、持续Goal暂停/恢复；真实模型报告 |
| CHAT-06 | [改问题、分叉试另一条路](01-current-and-gaps.md) | 保留/补强；部分验收 | 保留编辑/撤销/分叉与影响说明；副本分叉生命周期通过，bash撤销完整页面组合待补 |
| CHAT-07 | [审阅计划后再实施](01-current-and-gaps.md) | 已验收（脚本） | run-checks：goal-plan plan；10-31 无头 Chrome 验了这一轮结束后不刷新也能批准；原记录：结构化计划、版本CAS、明确批准实施和历史；goal-plan-plan及页面冲突恢复 |
| WORK-01 | [对照对话、文件、改动连续工作](01-current-and-gaps.md) | 已验收（浏览器） | run-checks：workpanel（准备资源）；10-30 点验工作面板 10 个入口；原记录：统一右侧工作标签、滚动与输入保留；15及页面回归 |
| WORK-02 | [确认这次究竟改了什么](01-current-and-gaps.md) | 部分验收 | run-checks：diff-lines；check-diff-review（25 项）是独立长流程，没在一条命令里；原记录：四种真实Git范围；25项diff回归，外部转换隔离 |
| WORK-03 | [指出某几行需要调整](01-current-and-gaps.md) | 部分验收 | 没有脚本；10-25 批次的定向证据；原记录：版本化行/文件反馈草稿；17及既有页面反馈证据 |
| WORK-04 | [确认、提交和交付修改](01-current-and-gaps.md) | 已验收（脚本） | run-checks：git-delivery；10-30/31 点验暂存、确认框、本地提交、提交后说明清空；原记录：文件级暂存/取消暂存、本地提交与回执；23、15项Git写入检查和页面确认 |
| WORK-05 | [点回答里的文件就能读](01-current-and-gaps.md) | 已验收（脚本） | run-checks：projectfiles、closeout（目录树分页）；原记录：目录树分页、文件链接、行号、高亮和只读边界；16/20 |
| WORK-06 | [检查命令并运行自己的验证](01-current-and-gaps.md) | 已验收（脚本） | run-checks：shell-access、operations、closeout-lifecycle；原记录：真实!命令回执、历史和独立项目操作；shell/lifecycle报告 |
| WORK-07 | [安全试改，再应用回去](01-current-and-gaps.md) | 已去掉（2026-10-31，用户决定） | 副本功能整体删除，见 [remove-copy-workspace.md](../remove-copy-workspace.md)；以下历史验收记录不再适用 |
| WORK-08 | [查看浏览器/子任务进展](01-current-and-gaps.md) | 已验收（脚本） | run-checks：substate；交互预览见 EXT-06；原记录：子任务过程/历史浏览器截图保留；交互预览另见EXT-06 |
| SET-01 | [少量操作完成配置](01-current-and-gaps.md) | 部分验收 | 没有脚本；10-25 批次的定向证据；原记录：八类设置复用原控件和作用域；真实页面分类验证 |
| SET-02 | [模型不可用时知道怎么解决](01-current-and-gaps.md) | 保留/补强；部分验收 | 保留供应商配置、错误与修复入口；新供应商/OAuth故障组合未重跑 |
| SET-03 | [启用 skill / MCP 并知道是否可用](01-current-and-gaps.md) | 保留/补强；部分验收 | Skill启用与来源版本已验；MCP沿旧能力，真实失联服务矩阵待补 |
| SET-04 | [连接 VM/远端并排障](01-current-and-gaps.md) | 保留/补强；部分验收 | 保留SSH连接/安装/诊断入口；本轮没有独立第二主机故障验收 |
| SET-05 | [知道页面是否已用最新程序](01-current-and-gaps.md) | 已验收（脚本） | run-checks：closeout（版本与 build 接口）；原记录：Web/backend真实版本和build接口；closeout报告及关于页 |
| UX-01 | [小屏和分栏都能操作](01-current-and-gaps.md) | 部分验收 | 没有脚本；10-25 批次 1280/1024/390/320 的定向检查；原记录：1280/1024/390/320定向页面检查，无整页横向溢出 |
| UX-02 | [视觉一致、能选主题](01-current-and-gaps.md) | 部分验收 | 没有脚本；视觉对齐见 18，要用户看截图；原记录：明暗主题、统一右栏/资源弹窗；真实截图检查 |
| UX-03 | [键盘与中文输入可靠](01-current-and-gaps.md) | 部分验收 | run-checks：permission-state；网页预览的输入法提交 10-31 验过；OS 输入法矩阵未覆盖；原记录：Tab真实焦点、Alt↑恢复人输入；组合输入事件守卫通过，OS输入法矩阵未覆盖 |
| SUB-01 | [主对话显示协作摘要：有几个子任务、各自负责什么、哪些仍在运行/需要处理](06-subagents.md) | 已验收（脚本） | run-checks：substate；原记录：真实分工摘要与父子状态；substate和子任务批次 |
| SUB-02 | [准确区分启动、工作、等待用户、停止中、完成、失败、未知；显示父任务在等谁](06-subagents.md) | 已验收（脚本） | run-checks：subcontrol；原记录：稳定尝试、waiting/stopping/terminal区分；subcontrol真实运行 |
| SUB-03 | [子任务详情先看结论/产物/限制，再按需展开执行过程；显示结果已返回父任务与否](06-subagents.md) | 部分验收 | 没有脚本；10-25 子任务批次的定向证据；原记录：结果回收/过程/限制说明；历史页面和真实完成记录 |
| SUB-04 | [父任务、子任务列表与详情间直接切换，保留位置和草稿](06-subagents.md) | 部分验收 | 没有脚本；10-25 子任务批次的定向证据；原记录：右侧父子导航和深链接；草稿/阅读位置保留 |
| SUB-05 | [用户明确选择“告诉主 agent”或“直接补充这个子任务”，并能只停止一个子任务](06-subagents.md) | 已验收（脚本） | run-checks：subcontrol；原记录：直接补充、只停目标、去重和采纳回执；subcontrol报告 |
| SUB-06 | [子任务审批/提问汇入主任务待处理区，显示来源且能直达](06-subagents.md) | 部分验收 | check-subinput 要人在网页上答，没在一条命令里；原记录：真实子任务提问/审批来源、过期拒绝；子任务批次 |
| SUB-07 | [展示执行目录、上下文来源与共享边界，提示父轮结束对子任务的影响](06-subagents.md) | 部分验收 | 没有脚本；10-25 子任务详情页的定向证据；原记录：实际目录和继承上下文说明；子任务详情页 |
| SUB-08 | [失败、已停止或结果未知时提供可理解的恢复路径，沿用同一子任务身份](06-subagents.md) | 保留/补强；部分验收 | 历史尝试、未知结果保留与恢复到主草稿；活跃目标直控，已结束由主agent沿原ID续跑 |
| EXT-01 | [命令面板与快速导航](07-workflow-completeness.md) | 已验收（浏览器） | 10-30 点验：命令面板（Ctrl+Shift+P）、搜索；原记录：操作/任务/文件/任务内查找、空输入恢复；20及页面回归 |
| EXT-02 | [活动中心与未读管理](07-workflow-completeness.md) | 部分验收 | 10-30 点验：活动中心“待处理 1”；check-activity 要人在网页上答；原记录：活动中心、账号阅读游标、待处理来源；11及既有真实证据 |
| EXT-03 | [上下文与用量管理](07-workflow-completeness.md) | 部分验收 | 没有脚本；10-25 批次 API+UI 压缩的定向证据；原记录：真实用量、未知额度、准备压缩及实际发送；API+UI compact成功 |
| EXT-04 | [显式调用 Skill](07-workflow-completeness.md) | 已验收（脚本） | run-checks：skill-refs；原记录：来源/path/hash固定引用、草稿恢复、失效拒绝和正文快照；Skill报告+UI |
| EXT-05 | [项目常用操作](07-workflow-completeness.md) | 已验收（脚本） | run-checks：operations；10-30 点验项目操作；原记录：动作配置、沙箱服务、Unix端口、日志、停止、副本初始化；9+8项实际检查 |
| EXT-06 | [网页预览与可视化反馈](07-workflow-completeness.md) | 部分验收 | P1 已实现并验收（[web-preview-p1.md](../web-preview-p1.md)：本机开发服务，能点、能输入、代理只放行授权 origin，连跑 5 次 18 项全过）；P2 agent 接管、P3 反馈、P4 沙箱服务未做；macOS 未实测；原记录：19的T01：命名空间方案在VM上与Chrome沙箱冲突；2026-10-31 改走“Chrome自有沙箱+固定目标代理”，原型通过（19文末），待补受限CDP与scheme拦截的原型后开发；截图回放不算完成 |
| EXT-07 | [产物预览与批注](07-workflow-completeness.md) | 已验收（脚本） | run-checks：highlight、closeout；10-25 批次的 PDF/批注证据；原记录：四种格式、PDF分页恢复、区域批注、版本过期、加密/损坏错误；20及真实页面 |
| EXT-08 | [工作区多标签](07-workflow-completeness.md) | 已验收（浏览器） | 10-30 点验工作面板多标签；10-31 预览标签；原记录：多资源标签与布局/恢复；15、既有和本次窄屏回归 |
| EXT-09 | [Goal 持续任务模式](07-workflow-completeness.md) | 已验收（脚本） | run-checks：goal-plan goal/budget/pause、goal-arbitration、closeout-lifecycle；10-30 点验持续 Goal；原记录：显式Goal、预算、暂停/恢复/修改、真实证据、重启和用户队列仲裁；21及六组真模型报告 |

## 验收记录

整体 A01–A42 未完成。以下仅记录已执行的局部证据，不提前关闭整个需求：

- Linux `go vet ./...`、`go build ./...`、全部页面 JS `node --check` 通过；隔离 clone 的 Darwin arm64 `go build ./...` 通过。
- `node spec/web-product/check-substate.mjs` 通过：后台 running 回执、完成/失败/停止优先级、等待、缺失终止记录、按 task_id 聚合、上下文未知和存档结果。该检查是纯展示状态回归，不替代真实模型。
- DeepSeek 实际派发两个后台子任务：继承上下文读取 README 首行、独立上下文执行 sleep；父任务用 agent_wait 收回结论。CLI 日志在 VM `/home/dev/.tern-web-product-qa/subagents-live.log`。
- QA 浏览器真实运行 `73846953-3e5a-421a-a9fd-46092d018a0b`：fork 子任务从继承的父对话读出 ORBIT_47，独立子任务 sleep 12 后返回 CHILD_READY；等待期间页面保持运行中，完成后显示结果。升级后的页面回读同一记录，显示“结果已返回主任务（不代表已验证或采用）”。A23/A24/A26 的这部分通过，其余场景仍待执行。
- 操作面板搜索设置并 Enter 打开、手动深色主题及刷新后保留已在真实页面核验。A31/A05 仅这些子场景通过。
- 截图目录（Mac）：`<Mac 上的 artifacts 目录>/web-product-20261025/`，包含 `subagent-running.png`、`subagent-inherited-result.png`、`subagent-result-collected.png`。

## 当前运行与下一步

- 隔离 QA clone：VM `/home/dev/workspace/tern/.data/selfuse/web-product-qa`，运行目录 `/home/dev/.tern-web-product-qa`（ext4），页面 `http://localhost:8095`；只使用仓库 QA 账号和必要模型配置，不复用日常数据。测试进程与源代码后续修改是不同版本，继续验证前同步 clone 并重建。
- 开发过程中另外出现 `internal/sandbox/launch_linux.go`、`spec/app-server-auto-replace.md` 和根 README 的 Linux 已删除可执行文件修正；这些不是本批编写的变更，保留且不归入本批成果。根 README 的网页导航段为本批新增。
- 当前已推进项目导航、草稿回执、活动中心、子任务提问恢复、下一轮队列，以及服务端当前轮补充和失败恢复，具体证据与缺口见下方各批记录。当前主线继续 Codex 视觉、布局和操作点对齐，以及工作面板/资源体验，其余需求保持完整目标。
- 子任务仍需补齐完整的尝试导航、等待审批路由、直控/恢复及全部异常验收；整项关闭仍以A01–A42完整矩阵为准；上表已按2026-10-28事实更新为实现与定向验收状态。

## 当前轮补充批次（2026-10-25）

实现契约见 [14-managed-steer.md](14-managed-steer.md)。agent 按 ID 去重，采纳回执随 v3 检查点保存；app-server 持久接收、查询与恢复，并将明确未采纳的内容转入队列。新网页使用服务端状态，旧 chat 和旧运行实例保持兼容。补充元数据保存失败时显示错误并阻止后续派发；“停止并发送”在已经停止中时只更新发送意图，不升级为 SIGKILL。

已验证（未提交代码，隔离 QA 真实 DeepSeek）：

- `check-managed-steer.py`：legacy、consumed、stop、immediate、unknown、natural、hook、storage、stopping 场景通过。报告为 QA 运行目录下 `managed-steer-<scenario>.json`。覆盖旧实例不盲发、双客户端去重、app-server 重启、停止保留、立即发送只处理指定组、异常退出不重做、自然结束仅转交一次、hook 原文清除与不可重排、写盘失败修复后凭采纳证据恢复、停止中再次发送不强杀。错误目标轮次拒绝已在 stopping 场景验证。
- 写盘失败为本测试任务 `.meta.json.tmp` 被临时目录占用，真实写入报错后恢复；不是磁盘断电、文件系统损坏或所有 I/O 故障的证明。unknown 场景真实 SIGKILL 本批自有 agent，未触及日常实例。
- 真实浏览器任务 `847669f9-b89a-42e8-9a22-d83173c0420f`：输入后清空草稿、显示待采纳；第二页与刷新看到同一状态；从第二页停止并发送后两页均清空待发区，实际仅一条 continue，回答 `UI_MANAGED_OK`。控制台无 error/warn。截图 `managed-steer-pending.png`、`managed-steer-result.png` 位于上文截图目录；报告 `managed-steer-ui.json`。
- 源码 Linux `go vet ./...`、`go build ./...` 和所有页面 JS 语法通过；隔离 clone Linux 可执行文件及 Darwin arm64 构建通过。该批次运行实例为 `/home/dev/.tern-web-product-qa/tern-steer2`；后续集成版本见下面的恢复收尾记录，web PID 始终以 `web.pid` 实际记录为准。

范围仍为 CHAT-02/03/05 与 A08/A09/A11/A12 的部分证据；尚未覆盖所有 SSH/网络故障、元数据与检查点全部崩溃窗口和完整跨版本矩阵，不关闭整个需求。下一批推进工作面板、资源标签与文件/改动反馈。

## 当前轮补充恢复收尾（2026-10-25）

契约见 [13-message-queue.md](13-message-queue.md)、[14-managed-steer.md](14-managed-steer.md)，可复用验收为 [check-managed-recovery.py](check-managed-recovery.py)。这批修复的是已经真实复现的三个边界：活跃 agent 被接管时队列误暂停；转交到下一轮后被提交规则拦截，continue 存档仍留原文；转交在命令解析等提交前阶段失败，原文丢失且被误标 delivered。

已实现：活跃轮次不按失败结局暂停；managed 转交的 continue 只记安全占位及 steer_ids，规则通过后才用真实 userMessage 展示正文。领取项保留原文直到提交/拦截证据出现，任意 exit 不能代替提交确认；失败或未知保留 claim 并暂停，不自动重发。核对释放要校验领取 ID、版本及实际结束状态，来源记录进入独立的 released 状态；只有明确释放且当前检查点确实缺失，下一次人的主动发送才可沿 resume_from 恢复前序上下文。发送回执不复制待确认原文。页面有恢复到输入草稿及明确释放入口；不自动发送，不回滚文件。

独立真实 DeepSeek 验收使用 `/home/dev/.tern-web-product-qa-review`（端口 8096），没有重启共享 8095，也没有使用脚本模型。`tern-recovery2` 的完整七项回归通过，报告为该目录的 `managed-recovery-fixed.json`：

| 场景 | 真实任务 | 证据 |
| --- | --- | --- |
| 活跃接管 | `3b0eea5a-9c17-4273-93dd-4dc01c71ca1d` | 原等待请求保留，队列未误暂停，回答后自动消费一次 |
| 自然转交后拦截 | `e877115f-181c-49bb-8897-bb238555a900` | blocked；存档、检查点、协议会话及回执不留受拦原文 |
| 停止并发送后拦截 | `ea12d564-787e-4687-a761-5070b27ebb27` | blocked，不重发 |
| 手动队列发送后拦截 | `389f9215-9656-40f6-b857-eaa8cfd4ae21` | blocked，发送回执只含标识 |
| 规则处理期间释放 | `c0b3fbff-2504-4c57-be67-42677dec2ac1` | 运行中释放返回 409，随后正常提交并清除 claim |
| 正常自然转交 | `2a633cec-ffc5-4b76-913d-f3255680f468` | userMessage 及真实模型输入含正文，正常回答，仅转交一次 |
| 提交前失败恢复 | `45a59c92-566d-42b4-ab76-6894f9411688` | 原文跨重启保留，错误版本/ID 拒绝，释放不重发；随后新请求完成，普通队列仍暂停 |

七项完成后再次重启并重提原 managed request_id，没有重复执行。随后将明确释放从 discarded 独立为 released，避免与确认拦截提示混淆；最终 `tern-recovery3` 对这一变化做了定向真实回归：任务 `632b10ea-079e-4826-a292-5f42be31a890` 的原文跨重启保留，释放时仍只有一条 continue，明确的新请求才新增第二条并返回 RECOVERY_RELEASED_OK，普通队列继续暂停。证据为 `managed-recovery-release-final.json`。七项报告保留原执行版本的状态值，不把最终定向回归冒充再次跑过完整矩阵。

该最终版本的 appserver `go vet`、Linux build、Darwin arm64 build、queue.js `node --check`、Python AST 与改动空白检查通过。独立验收结束后按 `/proc` 的 cwd 核对为 0 个存活进程。完整审查和逐项证据保存在 Mac `<Mac 上的 artifacts 目录>/web-product-20261025/managed-steer-review.md`。

本次集成后的只读源代码复核：`queue.go`、`steer.go`、`request.go`、`server.go` 和 `queue.js` 的最终恢复逻辑均保留，源码与 QA clone 中对应五个文件内容一致；读取共享 QA 的 web 进程，实际可执行文件为 `/home/dev/.tern-web-product-qa/tern-visual3`。未发现此次集成遗漏；这次只核对代码和已有证据，没有再次启动模型、服务或浏览器。

尚未关闭的边界：恢复草稿保留既有文字/附件及释放按钮的真实浏览器操作仍待整合页面验收。此前正常/拦截转交的浏览器展示已验证，不能替代新恢复入口的点击验证；独立 `review.localhost:8096` 被现有 Host 白名单拒绝，未放宽安全规则或再使用会共享登录 cookie 的 localhost 浏览器入口。本次只读扫描共享 8095 的存档，没有尚持有 managed claim 的真实任务可直接复用来验这两个按钮；独立恢复任务均已核对释放。SSH/完整网络故障、全部磁盘/崩溃窗口、复制标签页及完整版本组合仍保留待验收。旧版本已经丢失的输入不能凭空恢复。CHAT-02/03/05 与 A08/A09/A11/A12 继续保持开发中，不据此关闭完整需求。


## 项目导航批次（2026-10-25）

实现契约见 [09-project-navigation.md](09-project-navigation.md)。已增加目录身份、按主机隔离的项目筛选与项目树、个人置顶、执行上下文、项目行新任务/改名/移除入口，以及模型未配置时的发送拦截与原因提示。已有任务不再在输入区显示过期的工作区下拉，执行位置以顶栏实际目录为准。

证据：

- `check-navigation.py --home /home/dev/.tern-web-product-qa` 通过：真实 DeepSeek 任务返回 PROJECT_ID_OK；两个同 basename 不同目录的项目 ID 不同；重复登记拒绝；改名保持 ID 与历史执行目录；两账号置顶隔离；请求伪造 user 被登录身份覆盖；null/字符串 pinned 均拒绝。
- `--verify-existing` 在替换并重启隔离 web/app-server 后通过：任务目录与 ID、改名、两账号置顶均保留；删除项目登记保留文件目录，重新登记得到相同 ID。
- 浏览器使用 qa 登录，项目新任务入口正确预选项目，历史任务显示改名后项目名，项目筛选仍能定位历史任务；真实回答 PROJECT_ID_OK 可见。截图：`<Mac 上的 artifacts 目录>/web-product-20261025/project-tree-renamed.png`。
- 隔离 clone 的 Linux vet/build、Darwin arm64 build 与 shell.js 语法检查通过。QA 当前二进制为 `/home/dev/.tern-web-product-qa/tern-nav3`，web PID 以运行目录 `web.pid` 为准，勿按旧 PID 操作。
- API 验收报告：VM `/home/dev/.tern-web-product-qa/navigation-results.json`；任务 `1b57f16d-4222-4371-bc68-21048d5f7b87`。测试数据仅在 QA 运行目录。

范围限制：这批 A02/A03 是同主机目录与两账号验证，多主机、无目录老历史和副本场景还需补验；项目变更的跨页面实时广播、个人分组、Git 实时状态也仍待完成。NAV/CTX 等整项保持开发中，不因这批通过关闭需求。

## 草稿与发送恢复批次（2026-10-25）

实现契约：[10-drafts-and-delivery.md](10-drafts-and-delivery.md)。已实现按账号/主机/任务或项目分开的文字与附件引用草稿、输入选择恢复、多页面冲突保留/预览/合并、普通创建/续聊 request_id 去重与持久回执、未知结果查询、SSE id/after/历史边界与去重、Tab 焦点导航。旧页面账号与已登录账号不一致时服务端拒绝请求，保存原账号草稿再刷新。待采纳插话增加本浏览器恢复记录；恢复后先核对历史，不自动重发不确定的旧补充。

证据与限制：

- `check-delivery.py` 对真实 DeepSeek 创建与续聊各并发发送两次：两轮分别只存在一条对应 request_id 的 start/continue 事件；不同负载同 ID 拒绝；账号回执隔离、过期账号请求头拒绝、失败回执、SSE 游标精确补齐和负游标拒绝通过。最新测试任务 `44316423-c0b3-44a9-a224-cbb0cc130dc0`。
- 前一次测试任务 `9a246862-149f-4c21-8ec7-18d5bf768be9` 的回执在隔离进程重启后用 `--verify-existing` 验证通过；原请求重复提交返回同一任务，没有新增轮次。报告位于 VM `/home/dev/.tern-web-product-qa/delivery-results.json`（后续测试会更新报告）。
- 浏览器 A/B 任务分别填写 DRAFT_A/DRAFT_B，切回和刷新恢复正确；新任务在无项目/指定项目间分别保存 NEW_NONE/NEW_PROJECT，DeepSeek 选择跨刷新恢复。
- 两个真实浏览器标签同时编辑同一任务，TAB_A/TAB_B 都保留，出现另一版本预览和合并入口；点合并后输入包含两份文字。截图：`<Mac 上的 artifacts 目录>/web-product-20261025/draft-conflict-merge.png`。
- 第二账号 qa-secondary 打开同一任务，草稿为空，不显示 qa 草稿；填写 SECONDARY_PRIVATE_DRAFT 后 Tab 焦点进入附图按钮，没有发出消息。
- 网页普通发送真实返回 UI_SEND_OK，任务 `0d8282f7-913c-48f9-8922-05105daf1a56`，已发送草稿清空。没有用 UI 模拟成功或固定模型响应。
- Linux vet/build、Darwin arm64 build、页面 JS 语法与子任务状态回归通过。QA 最新已运行二进制为 `tern-drafts3`；之后又补了初始化输入锁和草稿读取失败时防串用，两处已做 JS 语法检查，尚待下一次构建后的页面回归。

仍需补验/继续实现：附件失效与上传中切任务、存储配额失败、发送响应丢失时网页回查、浏览器断网/SSH 中断/缺失退出记录、多页冲突在关闭与重新打开后的全部分支。该批次当时未覆盖的服务端插话持久化、队列原子消费及编辑排序已在后续批次实现，真实证据见“当前轮补充恢复收尾”和“下一轮消息队列批次”；不能以普通发送回执的测试结果代替这些独立验证。当前没有关闭 CHAT-02/03 的完整验收。

## 活动中心批次（2026-10-25）

契约见 [11-activity-center.md](11-activity-center.md)。增加活动面板、主机/项目/重叠状态筛选、单条与筛选集合批量已读、账号隔离阅读游标、分类通知设置和请求定位弹窗。摘要增加活动/结束序号和真实待处理请求标识；失联保留最后列表并标过期。请求事件入档后才广播等待变更，避免列表先拿到无定位的等待摘要。

已验证的局部场景：

- 真实 DeepSeek 任务 `c39cec73-5a02-4533-aa0c-64c0bb9dd33a` 发起 ask_user；活动中同时显示运行中和待处理。两个浏览器页的批量已读/手动未读同步；已读不清除请求。
- 从活动中的“回答问题”进入真实请求，选择“网页验收”，服务端只记一条 answer，模型继续完成。批量确认之后的新完成事件仍显示未读；刷新保留此前已读状态。
- 账号 qa-secondary 未继承 qa 对五条历史任务的已读游标；所有这些任务在第二账号仍显示未读。
- `check-activity.py` 验证真实 UI 答复已入档、waiting_requests 的事件标识与调用相符、并发两个回答仅一个被接收（204，另一条 409）、已结束请求再次回答仍为 409。证据在隔离运行目录 `activity-results.json`。
- Linux go vet/build、全部页面脚本 node --check、子任务状态回归通过；隔离 clone 的 Darwin arm64 构建通过。活动真实服务脚本在最终 Go 变更后的隔离构建再次通过。
- 首次脚本把成功状态误期望为 200，实际接口约定为 204；修正验收断言后通过，不修改接口来迎合脚本。
- 截图：Mac `artifacts/web-product-20261025/activity-question.png`、`activity-unread.png`。当前任务身份、请求和回答过程已视觉检查；`activity-center.png` 与 `activity-center-dark.png` 记录 1280×720 浅色/深色活动面板。

仍待验收：系统通知的实际桌面投递/跨页去重、真实 SSH 失联、多层子任务来源和审批路由、存储配额失败与所有视口。此批次时子 agent 尚未开放 ask_user；后续子任务提问进展见下一批记录，此条保留活动中心批次的验收边界。所有相关需求继续开发中。

本批自查：阅读只更新浏览器游标，待处理以服务端表为准；批量操作固定版本，等待请求不被已读清除；通知使用账号隔离的认领记录和跨页锁。修正了失联后列表恢复触发、跨任务残留请求弹窗、深色弹窗样式及历史 RPC 重用时跨轮错误定位的风险。未覆盖项保留，不作全部验收通过结论。

## 子任务提问批次（2026-10-25）

契约见 [12-subagent-input.md](12-subagent-input.md)。父任务开启 ask_user 时子任务继承并通过父进程转发，界面显示来源和独立回答入口。转发调用按子任务命名空间隔离，原调用 ID 保留用于工具轨迹及审计；网页回答带事件版本，错版本返回 409。交互请求取消沿父子链传播，服务端撤下权威等待并写 input_closed，已取消请求不在接管时重新派发。

已执行：

- 真实 DeepSeek 主任务 `4b8fe259-e2d3-422b-be42-c20a210495df` 同时派出 ALPHA/BETA 两个提问子任务。独立 task_id/call_id、事件请求 ID 与来源文本可核对。等待期间 SIGTERM 隔离 app-server，自动重连接管后两请求身份不变。
- 浏览器先回答 ALPHA=JSON：协作摘要为 ALPHA 已完成、BETA 等你回答，活动仍保留 BETA。随后回答 BETA=深色；检查实际子任务完成事件，各自结果含对应答案，父任务回收后结束，待处理清空。
- 真实任务 `51922a42-c718-47bb-8818-02319770f3e1` 再同时提问。向 ALPHA 携带 BETA 的事件版本回答被 409 拒绝且两请求仍在；只向经 PID/任务参数核对的 ALPHA 子进程发 SIGINT，其等待消失并产生 input_closed，BETA 和父任务继续工作，迟到回答 409。网页中旧回答控件失效；BETA=浅色仍可回答并完成。
- `check-subinput.py --mode start/restart/verify` 与第二组 start/cancel/verify 通过。证据：VM QA 目录 `subinput-answered-results.json`、`subinput-results.json`，前者保存完整两答案与重启验证，后者保存单子进程取消/错误版本拒绝/兄弟任务完成验证。
- Linux gofmt/vet/build、全部页面 JS 语法、隔离 clone Darwin arm64 构建通过。未增加 Go 测试文件，未使用脚本模型。

这仍不是 SUB-06/CHAT-04 全量验收：还需完整工具/联网审批竞争与失败恢复、旧客户端升级提示、窄屏及其他关联场景。当前实现只允许一层子 agent，不把未支持的多层嵌套写成已验证。

子任务工具审批回归：真实任务 `7bc27800-6386-4c30-a46b-fcaf40151bdd` 在 QA fixture 项目以 ask 权限派出 write_file 子任务。网页展示子任务来源、实际工具与文件参数，点击拒绝后只记一条 deny；目标文件未创建，子任务如实报告拒绝。审计中的 task_id 与 original_call_id 对应原始工具调用，路由使用独立转发 call_id。证据为 QA `subinput-approval.json` 和 Mac `artifacts/web-product-20261025/subagent-tool-approval.png`。这是工具拒绝路径验证，不代表允许/联网/并发失败路径全覆盖。

恢复验证另外发现：取消场景的父模型尝试在同一轮续跑 ALPHA，旧注册表错误拒绝并引发无进展保护，因此 `51922a42...` 的父轮最终未正常完成；此前通过的结论仅限取消隔离、迟答拒绝和 BETA 答复。已修正后台注册表，仅拒绝实际仍运行的实例，已终止实例可交给检查点恢复。未启动的续跑失败不再覆盖上一实例的真实状态。

首次修正后真实恢复任务 `f078fc13-9cd4-416c-8e2b-fe5be3d0c960` 已成功以同 task_id 启动第二次、context_mode=checkpoint，但模型选择在最终回答报告未答问题，没有重新使用 ask_user，因此不把它算作完整交互恢复通过。保留证据 `subresume-initial-results.json`，后续用明确要求恢复后重新提问并等待真实答案的任务验证。

最终恢复验收通过：真实任务 `e1f053ae-23d8-4b80-b379-4f60ea878bf4` 的同一子任务在同一父轮中被 SIGINT 中断后，沿原 task_id 续跑、context_mode=checkpoint；旧事件版本回答 409，新问题经网页选择“恢复确认”后只有一条有效 answer，子任务结果为 RECOVERY_RESULT，父任务 exit 0。`check-subresume.py --verify` 通过，QA 证据 `subresume-results.json`。浏览器显示一个逻辑子任务、两次尝试、沿用检查点和结果已返回；截图 `artifacts/web-product-20261025/subagent-resumed-result.png`，页面无捕获到的 error/warn。

本轮最终隔离构建为 `/home/dev/.tern-web-product-qa/tern-input4`（8095），日常 8090 与 ~/.tern 未改动。恢复注册表和页面状态修正后构建、JS 语法、check-substate 通过；相关完整需求仍为开发中，不把本次模型按明确恢复要求执行的成功推广为任意模型都会自动正确选择恢复路径。

## 下一轮消息队列批次（2026-10-25）

契约见 [13-message-queue.md](13-message-queue.md)。队列支持编辑、排序与版本冲突；领取和启动由服务端串行完成，持久 claim 与 queue_id 用于去重与重启核对。停止持久暂停队列，发第一条只消费一项、继续全部才继续后续；网页入队复用 request_id 和草稿回执。元数据并发事务同时修正，避免队列与改名/目标/归档互相覆盖。

已执行的局部证据：

- 真实任务 `1e095bc5-1295-4037-bb22-d2a1b6823197` 以 ask_user 保持运行，两个真实 HTTP 客户端并发追加无丢项，同 request_id 追加只有一项；编辑/排序竞争均为一个成功、一个 409；非法重复排列 400；改名与队列编辑并发均保留。
- 两个真实浏览器页面编辑同一项：B 保存后 A 保留自己的文字并显示服务器版本；A 旧版本保存 409，明确核对最新版本后才保存。上下移动同步到另一页；网页添加 QUEUE_FROM_UI 后输入清空且只追加一次。
- 网页停止保留 4 项；点击发第一条只发队首。后续 `check-queue.py --mode finish` 经 app-server 重启核验暂停、同一发送请求并发去重、单项与全部的区别；所有 4 个 queue_id 按确认顺序各有且仅有一个 continue 事件。证据 QA `queue-first-results.json`。
- `check-queue-recovery.py` 在停止的 QA 服务上构造两个存储窗口（明确是恢复夹具，不冒充真实断电）：已有启动事件但 claim/队列残留，以及无启动事件但 claim/发送回执待确认。真实服务恢复后前者去重、后者放回并暂停且回执变 rejected；两者都不新增执行事件。夹具项经版本校验删除。证据 QA `queue-recovery-results.json`。
- 恢复脚本首次将已退出但未被测试父进程回收的 zombie 当成仍在运行而报错；核验 /proc 后修正测试进程判断并回收，第二次完整通过。没有据观察超时重新启动仍运行的服务。
- Linux gofmt/vet/build、Darwin arm64 构建、页面 JS 语法通过；截图 `queue-edit-conflict.png`、`queue-edit-order.png`、`queue-paused.png`。最新构建修正了手动停止被显示为异常队列错误，以及各标签页编辑草稿互相覆盖的问题。

仍未关闭 A08/A09/A12：steer 的服务端持久化及转交失败恢复已经实现，后续真实证据见“当前轮补充恢复收尾”；仍需更完整的磁盘失败、真实 SSH 断线、旧版本 UI 能力提示、复制标签页与恢复草稿/释放入口的浏览器验收。当前不宣称整个可靠发送或所有停止行为已验收。

最新构建复验任务 `95014dd1-64a6-4c2e-8f80-bf29e38f695e` 再次通过并发 API 阶段。浏览器两个标签页分别写 DRAFT_TAB_A/B，第二页初始读取服务器内容而不是另一页草稿；第一页刷新再打开后恢复 A，第二页保存 B 后第一页仍保持 A，旧版本保存 409。明确核对后才保存 A。截图 `queue-draft-isolation.png`。

本批自查补充：领取项存不可变副本，避免启动失败后修改局部变量影响尚未落盘的 claim；暂停变更主动广播；发送回执重试剔除本地 notFound 提示字段，保持原负载。队列 claim 的恢复是核对证据后更新元数据，不自动启动模型。steer 的后续实现与恢复证据已补录，完整异常恢复及上述未验证场景仍保留待办，未缩小 47 项目标。

最新队列任务 `95014dd1...` 的 finish 阶段同样通过（QA `queue-results.json`）：停止/重启保留暂停，单项与全部消费按期望顺序，无重复 continue。最新源码补充暂停广播、不可变 claim 和原负载重试后再次通过 Go vet/Linux+Darwin build 与全部 JS 语法。QA 最终可执行文件 `tern-queue3`，仍为 localhost:8095；页面验收未捕获 error/warn，未提交或推送。


## 工作面板与项目文件批次（2026-10-25）

契约见 [15-work-panel.md](15-work-panel.md)、[16-project-files.md](16-project-files.md) 和 [17-diff-reading.md](17-diff-reading.md)。工作资源由一个标签面板管理，项目文件与任务临时产物明确区分；源码行号与 diff 两侧行号来自实际内容，引用先进入输入草稿。

已执行的证据与边界：

- 真实模型任务 `c2a1a233-69f4-4c69-849a-0d5ae20fdc77` 生成并实际检查两份 180 行任务产物，后续回复实际文件链接。模型根据工具说明使用 `/scratch`，没有按验收问题里的错误路径写进项目目录；验收脚本已按真实目录语义修正，未把这一偏差改写成模型严格执行原提示。
- `check-projectfiles.py` 验证相对/绝对项目路径、Unicode、目录内链接与内容版本；目录外链接、路径越界、FIFO、目录和不存在文件拒绝；二进制、空文件和超过 2 MiB 文件返回真实状态。检查没有额外模型请求，未放开宿主文件访问。
- panel1/panel2 的真实浏览器验证覆盖多文件/diff/子任务切换、展开与滚动恢复、刷新、关闭其他与恢复、跨任务草稿、文件链接到第 24/220 行、200 行分页、选行引用、HTML 按源码展示，以及 390px 布局。报告为 Mac 截图目录的 `workpanel-qa-report.md`、`workpanel-projectfiles-qa-report.md`。
- 独立审查发现并已修正：相同标签组合跨任务复用旧菜单闭包、项目搜索失败无限重试、A→B→A 返回后异步结果不刷新，以及无行号文件链接把阅读位置重置。源码检查通过；新增视觉版继续在真实页面复验相关入口，旧版功能截图不作为视觉对齐通过证据。
- `check-diff-lines.mjs` 验证多 hunk 的旧/新坐标、元信息和未跟踪文本；`workspace/diff` 增加预览内容 hash，未跟踪文件通过受限根目录打开并限制大小。完整 Git 范围、二进制/重命名语义与行反馈仍待完成，A14/A15 不关闭。
- Go vet、Linux/Darwin 构建及页面脚本语法已执行。新工作区读取没有增加 Go 测试文件，没有用 mock 或脚本模型替代实际执行。

## Codex 界面对齐批次（2026-10-25）

以 18 为当前约束。已重排侧栏、标题菜单、输入框、工作标签栏与源码预览。第一版真实页面暴露了初始化时查询暂时脱离文档的节点的问题，已修复并在 `tern-visual2/3` 实际登录和历史恢复通过；不以单纯语法检查代替页面运行。

独立审查后已修正隐藏筛选残留、菜单中的任务 ID 不可见、复制回执被收起、手机侧栏无法收起、菜单 Esc 冒泡停止、导入模型信息丢失、恢复工作标签时项目入口持续禁用及菜单重建丢焦点。进一步收敛输入控件为模型菜单，并在底部显示实际项目/副本与主机；最终浏览器证据在集成构建完成后补记。

本批尚未把整体视觉标为最终验收通过。官方素材不是当前安装版本的逐像素测量；待实际明暗主题、390/320px、菜单/键盘及真实等待任务验证后记录具体通过项。日常 8090 不因隔离 QA 升级自动切到本分支。


界面对齐定向验收补记：独立 UI agent 在固定 `tern-visual3` 上完成 1280×720 明暗主题、390×844/320×720、任务筛选清除、任务搜索、标签菜单键盘/焦点、宽度 480→520 刷新恢复、行号/文件信息和引用草稿验证，未捕获 error/warn。真实 DeepSeek 等待任务 `be403757-60a1-492b-81e6-ec796a454f77` 在三个菜单 Esc 后仍为 waiting=question 且无 exit；真实回答后得到 `VISUAL_MENU_ESC_OK`、exit 0。报告 `visual-qa-report.md` 及 `visual-qa-esc-result.json` 位于 Mac 截图目录。

后续 `tern-visual4` 将供应商/具体模型/推理强度合成一个入口，增加底部项目与执行目录菜单。主任务已真实操作新任务供应商/推理切换并还原、历史任务仅能调整推理而不伪装切模型、项目目录显示、原草稿保留、文件第24行跳转、明暗主题及320px菜单边界。320px项目菜单 x=24…312、模型菜单 x=59…309，页面宽320，发送按钮可达；控制台无 error/warn。最终截图为 `visual-final-{light,dark}-{chat,file}.png`、`visual-final-320-{project,model}.png`。

该批隔离服务为 `/home/dev/.tern-web-product-qa/tern-visual4`，后续版本见下方记录，PID以 `web.pid` 为准。这些证据证明本批布局和指定操作的实际状态，不关闭全部47项需求或宣称像素一致。后续继续按18对齐子任务、diff和设置流程；完整范围选择、行反馈、Git交付、Goal等未完成项仍保留。

最终集成检查：`tern-visual4` 的 Linux 构建、Go vet、Darwin arm64 构建和全部页面 JS 语法通过；`git diff --check` 无错误。模型/推理选择、主题及 viewport 已还原，无新增未结束的模型任务。预览保留在 localhost:8095，未提交、未推送。


## Git范围与反馈批次（2026-10-25）

`workspace/diff` 已提供工作目录、未暂存、已暂存、分支提交四种真实比较范围；分支采用指定base与HEAD的共同祖先。重命名两侧路径、unborn、冲突、二进制和预览截断有显式状态。行/文件反馈实现为本地草稿，加入主输入前重新核对，不自动发送。

- `check-diff-review.py` 在隔离真实Git目录和真实任务 `9805c9c2-ad05-403b-8f40-29f4e20edd3a` 累计通过25项检查。前20项在diff1，冲突修复与3类截断在diff2；没有声称全矩阵在diff2重跑。真实存档：start=1、model_requests=1、continue=0、exit=1，断点恢复没有新增模型执行。
- 覆盖暂存/未暂存叠加、重命名、中文/空格/字面路径、二进制、unborn、merge-base与base tip差异、detached HEAD、非法基准、无共同祖先、读前后仓库指纹不变、外部diff/textconv未执行、暂存删除后同名重建、CRLF及Git执行位规则。
- 冲突的workspace/staged/unstaged均返回单一文件并标conflicted；branch不混入index冲突。单文件超256KiB不返回半份patch；总量fixture 12项中4项截断，正文1,029,120字节；文件数返回500/501且标truncated。
- 报告位于VM `/home/dev/.tern-web-product-qa/diff-review-fixtures/4304bda535fb/report.json`，Mac副本 `artifacts/web-product-20261025/diff-api-report.json`。Git不枚举该fixture的FIFO，因此只证明不阻塞及文件集合匹配，没有验到FIFO notice；copy_base、并发Git变更仍未覆盖。
- 独立审查修复反馈保存/删除失败被误当成功、慢核对期间意见变化仍使用旧快照、跨页删除后旧内存副本复活，以及类型变化双patch的文件头被错标行号。两份JS语法与真实Git patch的纯解析检查通过；浏览器验证仍在进行，完整A14/A15不关闭。

用户后续提供三张当前Codex截图，并明确四类资源都在右侧栏；视觉实施以18的新结构为准，截图中聊天与目录信息不提交仓库。


## 用户截图对齐与右侧资源工作区（2026-10-25）

用户提供主聊天、子智能体列表、文件视图3张实际Codex截图，明确子智能体/文件/diff/网页都在右侧栏。按18调整了侧栏、淡蓝用户气泡、输入区左右操作位置、改动摘要/普通目标条、资源标签图标、文件面包屑，以及子智能体分组列表。未提交或推送。

- 当前QA集成为 `/home/dev/.tern-web-product-qa/tern-reference3`，web PID以 `web.pid` 为准（本次启动293570），8095。构建包含全部JS语法检查；`git diff --check`通过。8090与日常运行目录未改。
- reference2真实页面：任务c2a1a233在文件“显示更改”后进入右侧workspace范围；切到staged后再从文件进入仍明确回workspace。浏览器过程入口也留在右栏，当前任务无网页记录时显示资源为空；这不证明已经实现可交互网页预览。
- 同任务新侧24行反馈：输入后“稍后继续”，经重启/登录恢复原文字；编辑器紧邻第24行，下一个元素是第25行。保存不发模型；加入输入保留原草稿，重复加入无重复。切staged仍保留该意见并标来源范围。
- 临时修改QA文件第24行后，点击改动摘要实际重新读取，旧意见标为可能过期；加入输入仍保留旧片段CHANGED_024、旧行号和过期提示，不替换成新的CHANGED_024_QA_VERSION。测试后文件按完整原字节恢复，输入框恢复原草稿。
- reference2的1024×768页面：右栏为relative、宽465.6，聊天仍宽310.4，无全页横向溢出。桌面先收起侧栏，再切320×720并关闭右栏：主区/聊天均宽320，输入框x12–308，无0宽或全页横向溢出。
- reference3真实历史任务73846953：右栏总览“完成 · 2”，两个真实子任务有稳定身份图标与事件时间；详情显示继承来源、已返回父任务及真实结果，返回总览仍在右栏。主聊天条目明确显示已完成，控制台无warn/error。资源图标和状态文案是reference3最后增量；前述范围/反馈/窄屏证据来自reference2，未声称全矩阵又重跑。
- 截图位于Mac `artifacts/web-product-20261025/`：`reference3-agents.png`、`reference3-file.png`、`reference3-diff-feedback.png`、`reference2-320-chat.png`。该批仍不关闭整个47项/42场景。反馈存储故障、多标签并发编辑、实时子任务waiting→done、完整文件树/语法高亮、可交互网页预览仍需继续验收或开发。

## 2026-10-28 收尾进行中

用户再次要求收尾，明确选择由主 agent 单独完成，不使用子 agent。保留已有未提交改动，继续使用隔离 QA 运行目录，未操作日常 8090。最新实现契约见 [20](20-closeout.md)。

本次新增：服务端分页 `thread/find`、操作面板任务全文搜索/任务内查找/项目文件快开/空输入 Alt ↑ 恢复；设置八分类与运行 Web/backend 版本；用量详情和下一次正常发送前手动压缩；显式 Skill 的来源和内容版本引用、草稿、发送校验与正文快照。新 UI 和 Skill 全流程仍在验收，不提前关闭需求。

已完成的当次验证：

- 初始源码与 search/settings 增量的 Linux `go vet ./...` / `go build` 通过。
- `check-substate.mjs`、`check-diff-lines.mjs`、新增 `check-highlight.mjs` 通过；本地高亮还原原始 Unicode/HTML 实体及换行，超限/不支持类型回退。
- `check-projectfiles.py` 再次通过。
- 新增 `check-closeout.py --model`：目录树 200 条分页、205 项不重不漏、目录变化返回409、根外路径/管道/非法游标拒绝；任务历史分页边界固定、无重复事件、空/过长关键词和非法游标拒绝；运行 Web/backend build 一致。
- 真实 DeepSeek 任务 `af61140a-4907-42e5-ab0c-23ed0157ce6b`，手动 compact 请求1次，压缩后续聊保留 `CLOSEOUT_CONTEXT_731` 并返回 `CLOSEOUT_COMPACT_OK`。报告 `/home/dev/.tern-web-product-qa/closeout-results.json`。这是 API 验收，不替代新增压缩按钮的实际操作。
- 真浏览器：目录树与高亮可用，恶意 HTML 源码未执行；跨任务搜索 `PANEL_FILES_READY` 返回历史匹配；当前任务查找返回问题/回答事件位置；设置8分类和“关于”页真实运行版本正常。

QA 曾发生旧 Web 进程占端口而后端已更新，已按 `/proc/<pid>/exe` 核对并替换仅本次启动的QA进程；没有把混合版本页面记成最新UI通过。最终构建/全部JS检查/真实页面复验及47项台账仍待收尾。

收尾增量证据（closeout3/4）：

- `check-skill-refs.py` 真实 DeepSeek 任务 `74de996a-4500-4ad4-99be-b3d3965bfa44`：内容版本改变、同名来源替换、重复引用均拒绝且不创建任务；合法引用进入 start 元数据及本轮上下文，真实回答 `PINNED_SKILL_OK`；删除技能后的续聊拒绝。夹具只在 QA 的 skills 目录，结束后删除。报告 `skill-refs-results.json`。
- `check-subcontrol.py` 真实任务 `6412cb15-e111-4aa7-818d-709c771c5f53`：错误尝试拒绝，同请求去重、不同正文同ID拒绝；直接补充 ALPHA 后仅停止 BETA，ALPHA 的等待保留；BETA 迟到答复拒绝。脚本把成功回答的HTTP204错写成200而中断，核对真实事件确认回答已生效，修正断言后用 `--verify-existing` 读取原任务，没有重复创建。ALPHA 返回 `DIRECT_CHILD_731`，原子任务只有2次派发、1次真实回答；子进程退出后从检查点查到 consumed。报告 `subcontrol-results.json`，不把断点复核称为全脚本重新执行。
- 新增子任务直控通过父进程持有的真实子进程连接，不改变原有人工审批/权限；提供旧运行实例能力标记，停止只发中断，实际终态来自完成通知。状态未知保留输入，可恢复到主任务草稿核对，不自动续跑。
- closeout4 的 Linux vet/build、全部页面JS语法、子任务状态检查、git diff --check 通过。下一步继续真实页面及剩余功能，仍未提交或推送。

产物批次进行中（closeout5）：新增统一静态HTML/Markdown、图片、PDF.js逐页预览和本地版本批注。真实浏览器已打开2页PDF，切第2页后画布实际像素为蓝色（夹具第1页红、第2页蓝），无控制台错误。图片真实鼠标拖动到批注按钮区域后创建归一化矩形并打开编辑器；保存/加入输入、过期、损坏文件和任务临时产物路径继续验收。PDF依赖固定为pdfjs-dist 5.4.54，校验npm SHA-512后本地打包，许可证存third_party/pdfjs；不在运行时下载CDN。

计划与持续Goal真实验收（closeout7）：

- `check-goal-plan.py --mode plan`：`8f8dc01a-464c-499d-bd8a-d5b076eeac3e`，模型以plan权限创建2步结构化计划，过期版本保存拒绝；明确批准后执行两步并更新为completed，同一批准request_id只新增1轮。
- `--mode goal`：`4d129160-e57b-4254-8afe-8ce4af46a130`，显式启动后恰好2个自动续轮、2个不同queue_id；真实bash阶段证据关联到完成标准，Goal完成后不继续派发；重复开始请求去重、变更负载拒绝。
- `--mode budget`：`2950aa61-dde5-41b5-9658-273bbafc0836`，token上限1，保守闸门在上游模型请求前拒绝，Goal期间无request事件、used_tokens=0，进入exhausted，不自动扩预算。
- `--mode pause`：`b1f9cae2-599e-42cf-8560-392a73bc0777`，真实ask_user等待；暂停中断当前轮并禁止续发；修改标准/恢复后沿同一Goal ID完成，自动轮次累计为2没有清零。
- 报告在QA目录 `goal-plan-{plan,goal,budget,pause}.json`。上述均是真模型，不是伪造回执；完整重启/多页面/用户队列并存、墙钟预算和缺证据拒绝还需补验。

项目操作正在实现：配置hash校验、专用沙箱worker、只导出明确端口的Unix通道、跨轮生命周期与真实日志。普通 `!` 命令也接入request_id回执并在命令面板展示历史；不自动把服务日志送给模型。该批静态构建已通过，真实服务和网页入口仍在验收。


## 2026-10-28 收尾结果与验收边界

代码仍在 `feat/web-product` 的未提交工作区，HEAD保持 `6fba4ad6d8094fb837c60dc1cfa24a2d1d778119`。本次由主agent单独完成，没有派发协作agent；QA的父子任务是产品本身的真实功能验收。没有commit/push开发仓库，没有改日常8090或 `~/.tern`。

### 本次修正的实际问题

- 项目树/高亮及文件预览接线、设置分类、任务全文/历史分页搜索、显式Skill、手动压缩、子任务直控、四种产物预览批注、结构化计划、持续Goal、项目持久服务和Git交付已接通。
- Goal用户队列被误当自动轮次而要求进展报告的问题已修复；真实队列在两个Goal轮次之间只执行一次。预算检查重新核验目标后才中断所属轮次，终态耗时不再随查看时间增长。
- Git审核版本不包含每秒变化的作者时间戳；首次空提交拒绝。原生Git不支持HEAD符号验证与分支更新的组合事务，改为固定审核分支的旧OID比较更新。真实index锁不删除他人的锁；结果未知保留回执。
- 原生status/diff也可能执行clean/process。真实探针复现后，读取改用私有Git配置视图，暂存以无过滤对象/index plumbing完成；外部转换、行尾转换受限项明确提示。支持普通仓库的已暂存、未暂存、二进制、执行位、符号链接、字面路径和删除。
- PDF刷新恢复页码修复；加密/损坏文件不再停留在“正在读取”。静态HTML无脚本/同源权限；批注加入草稿时重新核对版本，保留过期定位。服务面板不因定时刷新反复重建未变内容，切回标签恢复读取。
- Alt↑恢复最近人的输入，跳过自动Goal调度消息；新资源弹窗和Git面板适配窄屏和主题。

### 实际运行证据

所有模型验收使用QA配置的真实DeepSeek，文件/Git/命令在独立夹具或副本中运行。报告副本位于 `<Mac 上的 artifacts 目录>/web-product-20261028/`，原始运行存档位于VM `/home/dev/.tern-web-product-qa/`。

| 报告 | 已执行范围 |
| --- | --- |
| closeout-results.json | 树分页/版本/路径边界、历史分页、版本信息、API压缩 |
| skill-refs-results.json | 内容/来源/重复引用拒绝、真实指定技能回复、删除后续聊拒绝 |
| subcontrol-results.json | 真实父子任务直发/停止/兄弟隔离/迟到答复拒绝与检查点采纳证据；原脚本HTTP状态断言修正后核对原任务，非全量重跑 |
| goal-plan-{plan,goal,budget,pause}.json | 计划批准、两轮Goal、硬token闸门、暂停修改恢复 |
| goal-arbitration.json | `f3465822-85b6-48f1-9c99-03084f5cd17b`，用户队列优先、缺证据实际拒绝、blocked不空转、结束不再派发 |
| operations-results.json | 9项真实服务/Unix端口/跨轮/日志/退出码/停止边界 |
| closeout-lifecycle.json | `a0b22565-650d-43d8-8896-0f8da2630960`，等待请求跨重启、无重复Goal轮次、墙钟耗尽、迟到答复、服务重启、shell去重、副本初始化部分失败；停止已退出服务返回409是正确结果，修正脚本后沿原任务续验 |
| git-delivery.json | final3的 `130bbe3f-a645-4290-9786-6ce1364e4a3a`，15项真实Git写入/拒绝/回执/不执行外部转换检查 |
| diff-review-final.json | `776a4951-f0d7-4641-ae0c-3b587ade7a03`，累计25项；unborn在final3前执行，组合/分支/冲突/各级截断在final3续跑，保留previous_errors，不冒称单次完整运行 |
| copy-delivery.json | `8d7c1778-4710-4232-b411-e16ed811e9f7`，原未提交内容纳入副本基线、冲突不改原目录、成功不提交、分叉共享副本/最后所有者清理；最终完整脚本通过 |

真实页面还验证了：Skill选择/刷新恢复/移除；手动压缩准备跨刷新，发送后出现真实compact并回答 `CLOSEOUT_CONTEXT_731 UI_COMPACT_OK`；Git按文件暂存与确认后本地提交（仅夹具）；项目命令失败退出码7与真实日志；计划版本/历史展示与并发保存拒绝保留编辑草稿；图片实际拖拽区域、PDF第2页像素与刷新页码、静态HTML脚本移除、加密/损坏PDF下载替代；修改文件后批注保留旧hash并明确过期，未调用模型，之后恢复夹具字节与主输入。1280×800浅色、320×720深色截图实看，390×844弹窗、1024×768分栏边界实测。Tab用真实键盘焦点验证；中文组合仅覆盖浏览器composition事件守卫，未声称各OS输入法全验。

### A01–A42收口口径

“定向通过”表示下列具体证据成立，不替代缺少的跨环境/故障组合。不能把上表实现状态直接当作42项全部通过。

| 场景 | 本轮/既有证据与剩余边界 |
| --- | --- |
| A01–A03 | 项目登记、同主机同名隔离、改名、个人置顶、搜索、草稿均有真实证据；两台独立执行主机组合待验 |
| A04 | 缺配置/断连保存输入的原实现保留；本轮没有全供应商与SSH故障组合 |
| A05 | 四视口、明暗主题、右栏/弹窗定向通过；非所有页面逐像素一致声明 |
| A06–A07 | 多任务/多页/账号草稿已有证据，Skill/压缩新增草稿回归通过；OS输入法矩阵待验 |
| A08–A12 | 真实插话/队列/审批/停止/幂等与重启已有报告；Goal与用户队列并存新增通过；真实SSH中断及所有磁盘崩溃窗口未全覆盖 |
| A13 | 既有编辑/撤销保留，副本分叉共享/删除保护通过；bash不可撤销边界的整组页面回归待补 |
| A14–A15 | 25项Git比较与既有行反馈页面、此次版本批注过期检查通过；过滤器明确受限，不伪造完整转换 |
| A16–A17 | 右侧多标签、目录/越界/恶意HTML/缺资源错误已有证据；PDF页码和文件错误新增通过 |
| A18 | 副本基线、冲突/成功、不自动提交和共享副本删除保护，最终API回归通过 |
| A19 | 设置分类及实际版本可读；个人/项目/平台全部组合仍沿旧设置契约，未全量复验 |
| A20 | 15项Git交付和真实页面确认通过；hooks/签名/转换/reftable的明确限制见23 |
| A21 | 真模型计划只读创建、批准去重、完成步骤、版本冲突及页面保留草稿通过 |
| A22 | 旧高级/观测/导入入口保留；不把UI重排说成新MCP/OAuth全链路验收 |
| A23–A30 | 真实子任务状态/提问/停止/直接补充/采纳及历史详情通过；旧实例和全部SSH重连组合仍有边界，结束后由主agent按原ID恢复 |
| A31–A34 | 搜索/快开/恢复输入、活动游标原报告、压缩API+UI、Skill来源/version与草稿通过；组合输入和跨主机范围按上述限制 |
| A35 | 沙箱持久服务、重复实例拒绝、端口导出、退出码、停止、重启、副本初始化部分失败通过 |
| A36–A37 | **受阻，未实现交互式网页预览**；见19的T01真实Chromium隔离失败，不禁用沙箱/放宽AppArmor绕过 |
| A38–A39 | 四类静态产物及批注、恶意/损坏/加密错误、版本过期、多标签和PDF刷新定向通过；超大文件复用已有读取上限 |
| A40–A42 | 六组真实Goal/计划验收覆盖多轮、预算、等待、暂停/改目标/恢复、重启、用户队列、缺证据、受阻和结束；极端断电/所有子任务与供应商协议组合未全验 |

### 当前阻塞与交付限制

1. EXT-06/A36–A37仍未完成：VM的AppArmor/bwrap隔离策略下Chromium无法保留浏览器沙箱启动（2026-10-31 补：改用“Chrome自有沙箱+固定目标代理”，原型在VM上通过，见19文末，阻塞解除，待开发）。需先提供受支持的独立隔离浏览器运行环境，再实现并验收交互控制、选区批注、用户接管与导航失效。未改变宿主安全策略，不用无沙箱Chrome替代。
2. 完整跨主机SSH、所有模型/OAuth/MCP失败、OS输入法、磁盘/断电矩阵仍未全验；现有局部证据均按实际范围保留。B5独立立项项未开发，未把它们混入本次完成声明。
3. 本轮深度自审已修复上述实际缺陷；结论为带环境与验收边界可审阅，**整体47项/42场景不宣称全量完成**。源码和验证报告可继续接手，不需要重做已完成部分。

最终校验补记：QA Web/backend为隔离clone构建的 `tern-closeout-final4`（8095）；Go vet、Linux构建、Darwin arm64构建、44份页面JS语法、Python验收脚本语法、highlight/substate/diff-lines检查与git diff --check通过。final4相对final3只修正PDF失败状态及控件，不把已有模型/Git套件说成再次全量执行。额外真实tracked-clean-filter探针 `git-filter-read.json` 验证diff不执行脚本、显示受限说明、index/HEAD不变。`ui-closeout.json`记录页面步骤与证据范围，原生截图在本对话工具输出，不虚构本地截图路径。计划页面并发冲突后刷新、明确采用新版本，原草稿最终保存为revision=2且approved=0，没有触发模型实施。所有新增服务实例均已退出/停止。

## 合并前复审与修复（2026-10-29，Claude Code）

复审后修了这几处：`settings.go` 的 gofmt；子 agent 继承 `-bash-host`（见 sandbox-modes.md）；Goal 只在设了 token 预算时才把模型请求串行，没设预算时子 agent 照常并行；Linux 沙箱在程序文件仍在原路径时直接 `--ro-bind` 挂载，只有程序文件已被删掉或换掉时才经 fd 复制；项目操作日志改为追加写，文件超过 1MiB 时换成最后 256KiB；app-server 启动时删掉超过 30 天的发送回执、网页 Git 操作记录和已结束的项目操作。「完全访问」仍允许任何登录账号选择（用户确认保持现状）。

验证（临时 `TERN_HOME`、端口 8097、真实 DeepSeek）：完全访问下模型 bash 与 `!` 命令在宿主机执行并写审计，沙箱模式仍隔离；host 父任务派出的子任务在宿主机执行；无预算 Goal 两轮完成并附 2 条真实证据；token 预算为 1 时停在 exhausted、没有上游请求；约 3MB 输出的项目操作日志文件 863KB、结尾完整；沙箱直接挂载 49ms、复制 81ms；启动清理只删过期且已结束的记录。前端仍只做了 `node --check`，没有在浏览器里逐项点验。
