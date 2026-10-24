# app-server 的约束

- `tern app-server` 是常驻进程，一个 `TERN_HOME` 一个，在 `.data/app-server/control.sock` 上等界面连进来。能连上 socket 的就是这个 `TERN_HOME` 的主人（目录 0700，经 SSH 时就是登录用户），不再另外鉴权。请求里带的 `user` 只用于设置的个人层和 agent 审计里的发起人，不是身份凭证。
- 一行一条 JSON-RPC 2.0，复用 `protocol.Conn`；每个连接第一条必须是 `initialize`，之后的请求才认。协议版本常量 `Protocol` 在这里，`internal/appclient` 引用它，这里不引用 `appclient`。
- 一个连接写不出去只卡住它自己：除 `initialize` 外每个请求各起一个 goroutine 处理，推送也各走各的 goroutine。`initialize` 不阻塞，在读循环里直接处理，紧跟它发来的请求才一定排在它后面。
- 用 `.data/app-server/lock` 的文件锁保证唯一，pid 写在 `app-server.pid`；`server/shutdown`、SIGINT、SIGTERM 走同一套收尾：删掉两个 socket 与 pid 文件再退出。agent 进程不动，重启后由新的 app-server 接管（启动时 `loadRuns` 并 `adopt`）。
- 只有平台空间：数据都在 `.data/` 下，不按租户分目录，启动 agent 时不传 `-tenant`。
- 设置：`.data/settings.json`，按 内置默认 › 平台 › 个人 合并，本机条件只会关掉工具；对话开始时把设置拍成快照写进 start 事件，续聊照快照启动。工作区登记的是本机 git 仓库，只有对话里选中的目录可读写。hooks 会在宿主机上以 app-server 的身份运行命令，只能由平台设置。
- 模型代理：`.data/app-server/llm.sock` 上跑 HTTP，路由 `POST /llm/<对话ID>`。启动 agent 时用环境变量交代运行环境：`AGENT_PROXY_SOCKET`（这个 socket）、`AGENT_PROXY_URL`（`http://tern/llm/<对话ID>`）、`AGENT_PROXY_TOKEN`（访问代理的令牌）、`AGENT_APP_SOCKET`（协议 socket）。代理按 agent 在 `X-Agent-Upstream` 里声明的地址转发，不复制路由表。没有固定端口：app-server 没有 `-addr`，同一台机器上还可以有好几个 `TERN_HOME`。
- 起 agent 和 `-skills-list`、`-mcp-list` 这类子进程用 `/proc/self/exe`（没有 `/proc` 时 `os.Executable()`），`-dev` 才用 `go run .`；这样 `go run . app-server` 拉起的进程在界面退出后也能起 agent。
- 审批与提问：agent 的 `network/requestApproval`、`tool/requestApproval`、`user/requestInput` 记成 approval 事件推给订阅者，任何界面回 `approval/respond`，先到的算数；人的审批先写审计，审计写不进去就不答复。不做 app-server 向界面发请求。
- 二进制内容（附件、沙箱文件、浏览器画面、导入的会话文件）按块传，每块原始数据不超过 4MB：`protocol.Conn` 一行最多 16MB，不改 `protocol` 包。
- 导入对话只接受 Codex 的 rollout 与 Claude Code 的会话 JSONL，只读、不续聊。
