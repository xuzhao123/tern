# tern 现状：进程、模块与数据链路

状态：说明（2026-10-23，对应 `app-server-auto-replace.md` 实现之后的代码），不是设计。代码改了结构，这份跟着改。

配图：[architecture.html](architecture.html)——同一份结构画成一张图（三层进程、连接层、模块与数据落点）。

## 1. 总图

三层进程：界面、app-server、agent。界面随人开关；app-server 常驻，管对话；agent 一轮一个进程，真正干活。

```text
 浏览器                                   终端
   │ HTTP + 登录 cookie                     │ 标准输入输出
   │ 事件流 SSE：GET /runs/<ID>/events      │
   ▼                                        ▼
 tern web（internal/web）                 tern chat（internal/chat）
   │ 登录、页面、把 HTTP 翻成协议方法        │ 斜杠命令、流式显示
   └───────────────────┬────────────────────┘
                       │ internal/appclient：一条连接、断了重连、订阅按 seq 补齐
                       │ 一行一条 JSON-RPC：initialize、thread/*、turn/*、approval/respond、settings…
                       │
       本机：.data/app-server/control.sock（Unix socket，目录 0700）
       远程：ssh -T <别名> → 远端的 tern app-server proxy → 远端的 control.sock
                       │
                       ▼
 tern app-server（internal/appserver）     常驻，一个 TERN_HOME 一个
   │ 对话存档、设置、审批、文件、附件、评测、审计
   │ 模型代理：.data/app-server/llm.sock
   │
   │ 每轮起一个 agent；agent 在 .data/live/<对话ID>.sock 上等 app-server 连进来
   ▼
 tern -agent-server …（internal/agent 等）  一轮一个进程；app-server 重启时照常跑，由新的接管
   ├─ 模型请求 → llm.sock（app-server 记录、转发）→ 方舟 / DeepSeek / Coconut
   ├─ 工具：计算器、时间、工作区读写、bash 沙箱、浏览器、ds_search、MCP、Skills、记忆、检索
   └─ 子 agent：再起 tern -agent-server 子进程，经标准输入输出说同一套协议
```

## 2. 一次对话怎么走

以网页端为例；chat 只是把第 1 步和第 6 步换成终端。

1. **界面 → app-server**：页面 `POST /runs`，网页端翻成 `thread/start`，经 `appclient` 写进控制 socket。远程时这一行先进 ssh，远端的 `app-server proxy` 原样转给远端的控制 socket。
2. **app-server 建对话**：`threadStart` 读设置（`.data/settings.json`，按 内置默认 › 平台 › 个人 合并），把这一刻打开的工具拍成快照，写成存档 `.data/runs/<对话ID>.jsonl` 的第一条 `start` 事件。
3. **app-server 起 agent**：`launch` 用 `/proc/self/exe -agent-server <快照转成的参数>` 起进程（`-dev` 时用 `go run .`），自成进程组，给它四个环境变量：
   - `AGENT_APP_SOCKET`：`.data/live/<对话ID>.sock`，agent 在这里等 app-server 连进来。
   - `AGENT_PROXY_SOCKET`：`llm.sock`。
   - `AGENT_PROXY_URL`：`http://tern/llm/<对话ID>`。
   - `AGENT_PROXY_TOKEN`：这一轮的随机令牌。

   进程号和令牌记在 `.data/live/<对话ID>.json`。app-server 连上 agent 的 socket，发 `session/attach` 和 `turn/start`（问题、之前各轮）。
4. **agent 干活**（`internal/agent/react.go` 的循环：请求模型 → 执行工具 → 结果回填 → 下一步）：
   - **模型**：`internal/llm` 组好请求，经 `llm.sock` 发给 app-server 的代理。代理核对 `X-Agent-Token`，把请求记成存档里的 `request` 事件，按 agent 在 `X-Agent-Upstream` 里声明的地址转发，流式响应原样回给 agent，结束后记一条 `response`。密钥由 agent 从 `TERN_HOME/.env` 读出、放在 `Authorization` 里，代理只转发、不记。
   - **工具**：在 agent 进程里执行。
     - `internal/workspace`：读、找、搜、改，边界是 `os.Root`。
     - `internal/sandbox`：bash，bubblewrap 沙箱，每个对话一个目录 `.data/sandbox/<对话ID>`；联网经 `internal/netguard` 的代理，不在白名单的域名要人批准。
     - `internal/browser`：无头 Chrome。
     - `internal/websearch`：ds_search，直连 DeepSeek。
     - `internal/mcp`：子进程或 HTTP。
     - `internal/skills`。
     - `internal/memory`、`internal/retrieval`：向量直连方舟，或用本地模型。

     大的工具结果存进 `.data/outputs/`。
   - **审批与提问**：agent 发 `tool/requestApproval`、`network/requestApproval`、`user/requestInput`。app-server 记成 `approval` 事件推给所有界面；谁先 `approval/respond` 算谁的，人的决定先写审计，再答复 agent。
5. **进度回到 app-server**：agent 发的通知（`turn/started`、`item/started`、`item/completed`、`turn/completed`…）每条带序号，app-server 写进存档。`item/delta`（逐字输出）只推给正在看的界面，不进存档。agent 自己另记一份会话记录 `.data/sessions/<第一轮ID>.jsonl`。
6. **app-server → 界面**：订阅了这个对话的连接都收到推送（`thread/resume` 建立的订阅）。网页端转成 SSE 推给页面，chat 打在终端里。
7. **一轮结束**：agent 写好检查点 `.data/checkpoints/` 后退出，app-server 记一条 `exit`，删掉 `.data/live/` 里这一轮的三个文件。续聊是下一轮：再起一个 agent，`turn/start` 里带上之前各轮的上下文和续接关系（`continues`，上一轮的检查点ID）。

## 3. 断开与重启时谁接着谁

| 断的是哪一段 | 怎么接上 | 代码 |
| --- | --- | --- |
| 界面 ↔ app-server（ssh 断了、app-server 重启） | 界面按 1、2、4…秒退避重连，最多 30 秒；本机每次先 `app-server start`，远程重新起 ssh；连上后每个订阅按最后收到的 `seq` 重新 `thread/resume`，app-server 补推中间的事件 | `internal/appclient/rpc.go` |
| app-server ↔ agent（app-server 退出或被换掉） | agent 照常跑，发出的消息编号留在内存里，模型请求等代理回来（最多 10 分钟）。新的 app-server 启动时 `adopt`：读 `.data/live/<对话ID>.json`，连回 agent 的 socket，按存档里最大的序号 `session/attach`，agent 补发之后的各条。agent 已经结束的，从会话记录补齐 | `internal/protocol/session.go`、`internal/appserver/live.go` |
| 没人连回来超过 10 分钟 | agent 当作界面断开：中断这一轮、写好检查点、退出；之后可以续跑 | `protocol.DetachGrace` |

## 4. app-server 的生命周期

- **谁起它**：网页端、chat、`tern app-server start` 启动时都调 `appclient.Start`：控制 socket 连不上，就用自己的程序以 `setsid` 在后台起一个（日志 `.data/app-server/app-server.log`，pid `.data/app-server/app-server.pid`，文件锁 `.data/app-server/lock` 保证一个 `TERN_HOME` 只有一个）。远程由远端的 `app-server proxy` 起。
- **什么时候换掉它**：连上的 app-server 是另一个程序（build 不同，build 是可执行文件的 sha256 前 12 位），又比这个界面先启动，并且没有评测在跑，就让它退出、换成自己的程序。正在跑的 agent 由新的接管，见上一节。同时启动的界面靠 `.data/app-server/start.lock` 排队。设计见 [app-server-auto-replace.md](archive/app-server-auto-replace.md)。
- **停**：`tern app-server stop`，或者 SIGINT、SIGTERM。agent 不停。

## 5. 模块

| 进程 | 包 | 管什么 |
| --- | --- | --- |
| 界面 | `internal/web`（页面在 `internal/web/page`，原生 ES 模块） | 登录（`.data/users.json`）、页面、HTTP → 协议方法、SSE、几台主机（`.data/web-hosts.json`）、内置的计算器 MCP（`/mcp`） |
| 界面 | `internal/chat` | 终端客户端：输入、斜杠命令、流式显示、插话 |
| 界面 | `internal/appclient` | 连 app-server：`Start`、`Stop`、`Proxy`、`Call`、`Subscribe`、重连、ssh |
| app-server | `internal/appserver` | 协议方法、对话存档、设置、审批、附件、文件、评测、导入、分叉与撤销、模型代理、起 agent 与接管 |
| 两边共用 | `internal/protocol` | 一行一条 JSON-RPC 的连接；agent 一侧可脱离、可补发的 socket 会话 |
| agent | `internal/agent` | ReAct 循环、工具分发与并发、确认与无进展检测、上下文管理与压缩、检查点、会话记录、子 agent、撤销 |
| agent | `internal/llm` | 模型协议（Chat Completions、Anthropic Messages、Responses）、路由表、重试、经代理时的请求头 |
| agent | `internal/workspace`、`internal/permission`、`internal/hooks`、`internal/commands` | 工作区读写与边界、权限规则与模式、生命周期 hooks、斜杠命令模板 |
| agent | `internal/sandbox`、`internal/netguard`、`internal/browser`、`internal/websearch`、`internal/mcp`、`internal/skills` | bash 沙箱、沙箱联网代理、浏览器、DeepSeek 搜索、MCP 客户端、Skills |
| agent | `internal/memory`、`internal/retrieval`、`internal/transcript` | 长期记忆、知识检索、导入对话（读 Codex 的 rollout 与 Claude Code 的会话 JSONL） |
| 都用 | `internal/audit`、`internal/telemetry`、`internal/tenant` | 只追加的审计（`.data/audit/audit.jsonl`，哈希链）、OpenTelemetry span（`.data/traces/`）、数据路径 |
| 命令行 | `internal/queue`、`internal/eval`、`internal/metrics` | 任务队列、评测与归因、核心指标 |

## 6. 数据落在哪（都在 `TERN_HOME/.data/` 下，`TERN_HOME` 没设时是 `~/.tern`）

| 路径 | 谁写 | 内容 |
| --- | --- | --- |
| `app-server/` | app-server | `control.sock`、`llm.sock`、`app-server.pid`、`lock`、`app-server.log`；界面的 `start.lock` |
| `runs/<对话ID>.jsonl` | app-server | 对话存档：界面看到的一切（start、通知、模型请求与响应、审批、exit） |
| `live/<对话ID>.{sock,json,err}` | agent、app-server | 正在跑的这一轮：agent 的协议 socket、进程号与令牌、崩溃输出 |
| `checkpoints/` | agent | 检查点：续跑、续聊、撤销的依据 |
| `sessions/<第一轮ID>.jsonl` | agent | 会话记录：agent 发出的协议消息，app-server 停着时用来补齐 |
| `outputs/` | agent | 超出预算的工具结果全文，模型用 `read_output` 读回 |
| `sandbox/<对话ID>/` | agent | bash 沙箱的工作目录，续聊各轮共用 |
| `attachments/` | app-server、agent | 图片，按内容寻址 |
| `settings.json`、`hub.json` | app-server | 设置；Skills 与 MCP 的登记 |
| `memory.json`、`memory-index/` | agent | 长期记忆 |
| `eval/` | app-server、`tern eval` | 评测记录 |
| `audit/audit.jsonl` | agent、app-server、网页端 | 审计（网页端只写登录、退出） |
| `traces/` | 各进程 | trace |
| `users.json`、`web-hosts.json` | 网页端 | 登录账号；页面上连过的主机 |

## 7. 不经 app-server 的入口

`tern -question …`（直接跑一个 agent，终端交互）、`tern queue`、`tern eval`（命令行）、`tern metrics`、`tern audit`、`tern rewind`。它们直接读写 `.data/`，不需要 app-server 在运行。
