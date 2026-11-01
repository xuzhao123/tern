# app-server 的约束

- `tern app-server` 是常驻进程，一个 `TERN_HOME` 一个，在 `.data/app-server/control.sock` 上等界面连进来。能连上 socket 的就是这个 `TERN_HOME` 的主人（目录 0700，经 SSH 时就是登录用户），不再另外鉴权。请求里带的 `user` 只用于设置的个人层和 agent 审计里的发起人，不是身份凭证。
- 一行一条 JSON-RPC 2.0，复用 `protocol.Conn`；每个连接第一条必须是 `initialize`，之后的请求才认。协议版本常量 `Protocol` 在这里，`internal/appclient` 引用它，这里不引用 `appclient`。
- 一个连接写不出去只卡住它自己：除 `initialize` 外每个请求各起一个 goroutine 处理，推送也各走各的 goroutine。`initialize` 不阻塞，在读循环里直接处理，紧跟它发来的请求才一定排在它后面。
- 用 `.data/app-server/lock` 的文件锁保证唯一，pid 写在 `app-server.pid`；`server/shutdown`、SIGINT、SIGTERM 走同一套收尾：删掉两个 socket 与 pid 文件再退出。agent 进程不动，重启后由新的 app-server 接管（启动时 `loadRuns` 并 `adopt`）。界面启动时会把程序不同、又比自己先启动的 app-server 换掉（`appclient.Start`，靠 `initialize` 回复里的 `build`、`started`、`dev`、`evals`），所以这层接管不能坏：改 agent 协议或 `adopt` 时，旧 agent 要能被新的 app-server 接上。评测的试次不能接管，有评测在跑时界面不换。
- 只有平台空间：数据都在 `.data/` 下，不按租户分目录，启动 agent 时不传 `-tenant`。
- 设置：`.data/settings.json`，按 内置默认 › 平台 › 项目 › 个人 合并，本机条件只会关掉工具；对话开始时把设置拍成快照写进 start 事件，续聊照快照启动。项目层是所选工作区根目录的 `.agents/tern.json`（扁平的 JSON 对象，值都是字符串），只认 `provider`、`model`、`reasoning_effort`、`permission_mode`、`permission_rules`、`net_allow`、`max_steps`、`context_window`、`tool_output` 与工具开关，其中 `permission_rules`、`net_allow` 是追加不是覆盖；`hooks`、`workspace`、`embedding`、`memory_ttl`、`sandbox_mode` 与不合规的值不生效，设置页列出来，真正进了快照的键值记在 start 的 `project_config` 上（被个人层或 `thread/start` 明确给的启动参数盖掉的不记）。`sandbox_mode` 只允许平台层与个人层，项目层不能要求「打开就完全访问」。工作区登记的是本机 git 仓库，只有对话里选中的目录可读写。hooks 会在宿主机上以 app-server 的身份运行命令，只能由平台设置。
- 模型代理：`.data/app-server/llm.sock` 上跑 HTTP，路由 `POST /llm/<对话ID>`。启动 agent 时用环境变量交代运行环境：`AGENT_PROXY_SOCKET`（这个 socket）、`AGENT_PROXY_URL`（`http://tern/llm/<对话ID>`）、`AGENT_PROXY_TOKEN`（访问代理的令牌）、`AGENT_APP_SOCKET`（协议 socket）。代理按 agent 在 `X-Agent-Upstream` 里声明的地址转发，不复制路由表。没有固定端口：app-server 没有 `-addr`，同一台机器上还可以有好几个 `TERN_HOME`。
- 起 agent 和 `-skills-list`、`-mcp-list` 这类子进程用 `/proc/self/exe`（没有 `/proc` 时 `os.Executable()`），`-dev` 才用 `go run .`；这样 `go run . app-server` 拉起的进程在界面退出后也能起 agent。
- 审批与提问：agent 的 `network/requestApproval`、`tool/requestApproval`、`user/requestInput` 记成 approval 事件推给订阅者，任何界面回 `approval/respond`，先到的算数；人的审批先写审计，审计写不进去就不答复。不做 app-server 向界面发请求。
- 二进制内容（附件、沙箱文件、浏览器画面、导入的会话文件）按块传，每块原始数据不超过 4MB：`protocol.Conn` 一行最多 16MB，不改 `protocol` 包。
- 导入对话只接受 Codex 的 rollout 与 Claude Code 的会话 JSONL，只读、不续聊。
- 项目身份：`workspace/list` 和设置回复带按绝对目录计算的稳定 `id`；任务摘要用快照的 `workspace_dir` 算 `workspace_id`。显示改名不重写历史快照。没有目录证据的历史记录不得按同名推断关联。
- 个人置顶：`thread/pin` 只改 settings.json 的 `thread_pins[user]`，持 hubMu 原子保存；`thread/list` 按当前用户返回 `pinned`，不改变任务活动时间。web 的 user 来自认证账号，协议层仍是本地主人信任边界。
- 对话的名字、归档、旁支、排队的后续消息与目标放在 `.data/runs/<对话ID>.meta.json`（`{"name": "…", "archived": true, "side": true, "queue": [{"id": "…", "text": "…", "at": "…"}], "goal": {"text": "…", "at": "…", "sent": false}}`，先写 `.tmp` 再 rename），不写进存档 `.jsonl`：存档在 `exit` 时就关了，改个名也不该把对话顶到列表最上面。`thread/delete` 删存档、检查点、快照、浏览器画面，以及按第一轮存的沙箱目录、工具输出与会话记录；被分叉对话引用的轮次文件不删（分叉复制了原对话前几轮的事件，共用这些轮次ID和沙箱目录）。
- 目标（`thread/goal/set`，网页端标题下面那一行）同样在 `.meta.json` 里，`summary()` 带 `goal`（只有文字）。设了、改了或者清除了以后，下一轮开始时 `notes()` 里带一句提醒，`turn/start` 发出后把 `sent` 记成 true，所以只说一次，不是每轮重复；清除的提醒说过就把这一项去掉；清除一个还没说给模型的目标（或者本来就没有）不留这一项，直接去掉。
- 排队使用 thread/queue/add/update/delete/reorder/send/recover，队列及版本、领取项、暂停状态在 .meta.json；元数据的读改写持同一把 r.mu。编辑/排序与新发送按 version 校验，普通发送与领取由 turnMu 串行化；先保存 claim，再 fsync 带 queue_id 的启动事件，最后起 agent。只在正常结束且未暂停时自动续发；停止持久暂停，普通续聊不解除，发第一条只消费一项，继续全部才解除暂停。启动前校验失败放回并暂停；已有启动事件的项不能自动重做，错误在历史中保留。重启核对 claim/queue_id，不凭进程句柄消失猜执行结果。managed steer 同样由服务端持久接收，按目标轮次核对采纳/未采纳证据后转入队列；异常未知不自动重发，写盘失败阻止后续派发。旧 unmanaged 行为继续兼容。
- 分叉（`thread/fork`）把原对话第 n 轮及之前的事件原样复制进新对话的存档（`seq` 不改），最后追加一条 `fork` 事件（`body.from` 是原对话）；原对话与文件都不动。分支和原对话共用工作区与 bash 沙箱目录，所以分支的 `turn/start` 按 `fork`、`rewind` 事件生成 `notes`，提醒模型磁盘上的文件可能和记录不一致。分叉时带过来的轮次不能在分支里撤销：agent 的撤销沿磁盘上的续接链往后找，分支还没跑过时会走进原对话。

- 普通创建/续聊可带 request_id：handle 的 requestOnce 在执行前持久认领 `.data/requests` 回执，相同账号/ID 不重复执行，负载不同拒绝。start/continue 事件带 request_id/request_user 以支持回执不完整时回查；`request/status` 的 pending 不能自动当作未执行重做。旧客户端不带 ID 时仍按原路径处理。

- thread/list 摘要兼容携带 activity_seq、completion_seq、waiting_requests。当前等待来自审批/提问表，事件只补定位；新请求先入档再广播 waitingChanged，旧 waiting 字段保留。已读与通知属于浏览器偏好，不改变等待状态。

- request/cancelled 按 RPC ID 清除对应交互等待，写 input_closed 事件后广播；回答可带 request_id=event:<seq>，在认领等待的同一锁内核验事件版本，过期版本返回 409。子任务转发的 original_call_id 用于审计与模型工具调用对照，call_id 用于网页请求路由。

- workspace/file/read 的根只取任务快照 workDir，经 os.Root + 非阻塞普通文件检查，拒绝根外路径和链接；完整预览限制 2 MiB，返回读取字节的 SHA-256 与修改时间，过大只返回元数据。预览不修改模型的 seen 账本。workspace/diff 的 view_version 仅标识返回的预览内容，不充当仓库写锁或整仓事务版本。
- workspace/diff 支持 workspace/unstaged/staged/branch，默认 workspace；branch 必须指定可解析且与 HEAD 有共同祖先的 base，比较 merge-base→HEAD。查询不暂存/提交，禁用 external diff/textconv 与 Git optional locks；冲突、二进制、重命名、未跟踪和各级预览截断显式返回，不把未完整预览说成无变化。
- thread/find 分页只读任务问题/最终回答/工具结果/人执行命令，固定 through 事件边界，不搜索系统提示与原始模型输入。显式 skill_refs 核对任务快照允许的名称、当前启用状态和解析器返回的 path/version；正文作为当前轮 notes 的快照，事件只记引用，不提升工具权限。
- `thread/plan/*` 与 `thread/continuous/*` 的事实源为runMeta，更新带revision。持续Goal只由显式用户操作开启，经原队列和turnMu派发；自动Goal轮次需进展报告，用户队列优先且仍计预算。完成证据必须关联本Goal期间真实成功工具事件，停止不自动扩预算或续跑。
- `workspace/operations/*` 仅在Linux为任务快照目录启动独立沙箱worker，先审计并认领request_id；服务实例、日志和PID启动身份保存在私有services目录，重启不自动重跑。`initialize` 动作不再支持，读配置时整份拒绝。导出端口只走私有Unix socket。
- `workspace/git/*` 只写任务实际仓库，暂存先准备临时index再版本核对和原子替换；提交只更新审核的固定分支并比较旧OID。不要直接git add或以原配置运行status/diff：它们也可能执行filter。`safeGitReadEnv`使用私有配置视图；不拷贝外部命令配置。未知写入结果查原日志，不盲重放。

- 旧任务显式命令启用由thread/bash/enable写入meta.bash_enabled，先审计、不启动模型、不提升权限；下一轮快照取原Bash或显式启用。shellCommand接收本次permission/sandbox并校验，缺省沿旧快照；工具关闭时不能只传host就执行。meta空值删除判定必须计入bash_enabled。
