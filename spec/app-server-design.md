# app-server 与 SSH 远程：照 Codex 的做法

状态：已确认（2026-10-23），第 14 节是答复过的问题。第一步与第一期已实现（2026-10-24），第 13 节的验收全部通过；第二期、第三期未做。取代 [remote-agent-design.md](remote-agent-design.md)（搁置）；调研与对比见 [remote-control-research.html](remote-control-research.html)。

## 1. 目标与范围

在手边这台机器（下称本机）上用网页端、终端、以后的桌面端，操作另一台机器（下称 devbox）上的代码。做法照 Codex 的 SSH 远程：

- devbox 上常驻一个 `tern app-server`，由它管对话：新建、续聊、列表、审批、停止都经过它。agent 进程、代码、模型密钥、设置、存档都在 devbox。
- 本机的界面经 SSH 连它：`ssh devbox tern app-server proxy` 把 SSH 的标准输入输出接到 app-server 的 Unix 控制 socket。不开端口、不需要公网，鉴权就是 SSH。
- SSH 断了只断 proxy；app-server 和正在跑的任务照常，界面重连后补齐中间的过程。
- 同一台机器上的界面也连同一个 app-server（直接连 Unix socket），所以终端和网页端看到的是同一批对话，可以同时看同一条。
- 界面与 app-server 之间说 JSON-RPC，方法和生命周期照 Codex app-server（`initialize`、`thread/*`、`turn/*`）。
- 网页端现在的全部功能，远程模式下都能用。

不变的：agent 仍然一轮一个进程；检查点、存档格式、沙箱、权限、审批与提问的流程不变；页面的样子和交互不变（只在顶栏加主机与连接状态）。

第一期不做：一个网页端同时连多台主机、分叉、桌面端；同时去掉租户账号（第 8 节）。

## 2. Codex 是怎么做的（依据）

- app-server 常驻，监听 `CODEX_HOME` 下的 Unix 控制 socket；`codex app-server daemon start` 幂等，返回时 socket 已经能答 `initialize`（官方 `codex-rs/app-server-daemon/README.md`）。
- 桌面端经 SSH 在远端运行 `codex app-server proxy`，把 SSH 的标准输入输出接到控制 socket（社区 issue openai/codex#23527 的说法，官方文档没写）。
- 终端 `codex --remote unix://` 连同一个 app-server；对话的实时状态归正在运行它的 app-server。
- 对话存在 `CODEX_HOME`；没有订阅者、也没有活动满 30 分钟才从内存卸载。
- 远端要先装好 codex、在登录 shell 的 PATH 上、登录好；只认 `~/.ssh/config` 里具体的 Host 别名，带通配符的忽略。

## 3. 架构

```text
 本机                                             devbox
┌───────────────────────────────┐              ┌──────────────────────────────────────────────┐
│ 浏览器 ─ tern web（界面）      │              │ tern app-server（常驻，一个 TERN_HOME 一个）   │
│ 终端   ─ tern chat（界面）     │── ssh ─────▶ │   控制 socket  .data/app-server/control.sock  │
│ 以后   ─ 桌面端（界面）        │  标准输入输出 │   对话、存档、设置、审批、文件、记忆、评测…   │
│                               │              │   模型代理     .data/app-server/llm.sock      │
│ 每个界面起一个：               │              │        │ 每轮起一个                           │
│ ssh -T devbox … tern app-server proxy        │        ▼                                      │
└───────────────────────────────┘              │   tern -agent-server …（agent）               │
                                               │   文件工具 · bash 沙箱 · 子 agent · 工作区     │
                                               └──────────────────────────────────────────────┘
```

本机模式是同一张图去掉 SSH：界面直接连本机的控制 socket。

| 角色 | 命令 | 生命周期 |
| --- | --- | --- |
| app-server | `tern app-server` | 常驻；同一个 `TERN_HOME` 只能有一个 |
| agent | `tern -agent-server …`（现在的 `-app-server` 改名） | 一轮一个，由 app-server 启动 |
| 界面 | `tern web`、`tern chat`、以后的桌面端 | 随人开关；连本机或经 SSH 连远端 |

`internal/web` 里读写数据的代码全部搬进 app-server。网页端只剩登录、页面，以及把 HTTP 请求翻成协议调用；它自己只读 `.data/users.json`，另外照旧把登录、退出写进本机的审计（账号在本机）。

## 4. 前置：TERN_HOME 与 agent 参数改名

**TERN_HOME**：现在 tern 只能在源码目录里跑。网页端检查当前目录有没有 `go.mod`（`internal/web/server.go:163`）；`.env`（`internal/llm/protocol.go:153`）、`.data/`（`tenant.Path`）、`skills/`、`retrieval/corpus.md`（`internal/retrieval/retrieval.go:158`）、`eval/cases.jsonl`（`internal/eval/run.go:118`）都按当前目录找。经 SSH 在远端拉起 app-server 时，当前目录是家目录，所以要有固定的数据目录。

- 新环境变量 `TERN_HOME`。`main` 最前面（分派子命令之前）做三件事：
  1. 记下原来的工作目录。
  2. 按下一条的规则算出 `TERN_HOME`，目录不存在就建（0700），写回环境变量 `TERN_HOME`，让子进程继承。
  3. chdir 到它。
- 例外：`sandbox-init` 在这三件事之前分流，不碰 `TERN_HOME`。它是沙箱里的第一个程序，环境变量已经清空，照默认规则会去找甚至新建 `~/.tern`。
- 之后所有相对路径照旧，不逐处改。
- agent 自己的路径参数 `-workspace`、`-image`、`-hooks` 解析完后，相对路径按原来的工作目录转成绝对路径。
  - 写进检查点的启动参数（`agent.CheckpointArgs`）里，这三个参数的值也换成绝对路径，`-x 值` 和 `-x=值` 两种写法都要处理。否则换个目录 `-resume`，相对路径会按新的目录解释。
  - 子命令（`web`、`chat`、`queue`、`eval`…）的参数不做这一步，相对路径按 `TERN_HOME` 解释。
- 没设 `TERN_HOME` 时：当前目录是 tern 源码根目录就用当前目录，现在的用法不变；否则用 `~/.tern`。判断源码根目录的条件是当前目录的 `go.mod` 第一行 `module` 是 `github.com/xuzhao123/tern`。
  - 这个判断只写一份，放在 `internal/tenant`（数据路径都由它决定），例如 `tenant.SourceRoot(dir)`。`main` 和网页端的 `-dev` 检查共用它，第一期的 app-server 也用它。
- 网页端去掉 `go.mod` 检查（`internal/web/server.go:163`）和“请在项目根目录运行”的报错，改用上面的规则。`-dev`（`go run .`）要求 `TERN_HOME` 是源码根目录，不是就报错退出。
- 新的 `TERN_HOME`（比如第一次用的 `~/.tern`）里还没有 `.data/`，写文件之前要先建目录。现在漏了这一步的是 chat 的日志文件（`internal/chat/chat.go:370` 直接 `OpenFile`，报“打不开日志文件”）：改成先 `MkdirAll(filepath.Dir(…), 0o700)`，同其他写 `.data` 的地方。
- 不在 `TERN_HOME` 里的只有工作区：设置里登记的是绝对路径。
- `~/.tern` 里没有 `skills/`、`retrieval/corpus.md`、`eval/cases.jsonl` 时，对应功能照现在“文件不存在”的情况处理，不另做安装。

**agent 参数改名**：常驻进程叫 `tern app-server`，agent 的 `-app-server` 参数改名为 `-agent-server`，免得两个名字混在一起。要改的地方：
- `main.go:121`（参数定义）、`main.go:684`（检查点参数里过滤掉它；旧检查点里本来就没有它，不用迁移）。
- `internal/web/server.go:630`：第一步先改名，第一期随 `launch` 搬进 app-server。
- `internal/chat/chat.go:88`、`chat.go:361`：第一步先改名，第一期 chat 改成客户端后去掉。
- `internal/agent/child.go:73`。
- README、`internal/protocol/AGENTS.md`。

环境变量 `AGENT_APP_SOCKET` 不改名。

这两项可以先单独合并：不碰协议，开发时的用法不变。

## 5. 协议

**传输**：一行一条 JSON-RPC 2.0，复用 `protocol.Conn`（和 agent 协议同一种写法，带 `"jsonrpc":"2.0"`；Codex 省掉这个字段，这里不跟）。控制 socket 在 `TERN_HOME` 下的 `.data/app-server/control.sock`，目录 0700；用相对路径，免得超过 Unix socket 路径 108 字节的上限（同 `internal/web/live.go` 的 `livePath`）。

**握手**：每个连接第一条是 `initialize {client: {name, version}, protocol: 1}`，回 `{server: {version, protocol, host, home}}`。协议版本对不上就拒绝，并报出两边的版本号（远端装的 tern 和本机不同时会遇到）。
- `protocol` 是整数，写成 `internal/appserver` 里的常量，改了方法或参数的含义就加一。只比它。
- `version` 只用来显示：取 `debug.ReadBuildInfo` 的 `vcs.revision` 前 12 位，`vcs.modified` 为真时加 `+dirty`；取不到（如 `go run`）写 `devel`。
- `host` 是 `os.Hostname()`，`home` 是 `TERN_HOME` 的绝对路径。
- 没先 `initialize` 就发别的请求，回错误“先发 initialize”。

**错误**：方法出错时回 JSON-RPC 的 error。
- 现在网页端返回什么 HTTP 状态，`code` 就填那个数（400、404、409、413、503 等），`message` 是现在的报错文字。网页端照 `code` 原样还原成 HTTP 状态。
- 协议本身的错误（解析失败、没有这个方法、没先 `initialize`）用 JSON-RPC 的标准负数码。

**身份**：能连上控制 socket 的就是 `TERN_HOME` 的主人：socket 目录 0700，经 SSH 时就是 SSH 登录的那个用户，不再另外鉴权。app-server 只有平台空间，数据都在 `.data/` 下。请求里可以带 `user`（网页端填登录账号名），用于设置的个人层和 agent 审计里的发起人（`AGENT_USER`）；chat 不带，就是没有个人层。

**对话**：一条对话（thread）就是现在网页端的一个 run：ID、事件列表，存档 `.data/runs/<ID>.jsonl` 格式不变。事件内容沿用现在的 `event`（页面靠它渲染），不改成 Codex 的 item 格式。

**二进制内容分块传**：附图最大 20MB（`llm.MaxImageBytes`），base64 后超过 `protocol.Conn` 一行 16MB 的上限（`internal/protocol/protocol.go:70`）。所以附件、沙箱文件、浏览器画面、导入的会话文件这类内容按块传，每块原始数据不超过 4MB（base64 放在 `data` 字段）。不改 `protocol` 包。
- 上传（`attachment/upload`、`files/write`、`thread/import`）：同一个方法调多次。
  - 第一块不带 `uploadId`，`offset` 为 0；回复里带新的 `uploadId`，之后每块带上它。`offset` 必须等于已经收到的字节数，不等就回 400。
  - 带 `done: true` 的那块收到后，才走现在的处理（检查、写审计、存盘），回复同现在的接口。其余参数（文件名等）放在 `done` 那块里。
  - 收到一半的数据放在内存里，归这个连接所有，连接断开就丢掉。
  - 累计超过现在接口的上限（附图 20MB、文件面板 10MB、导入 128MB），就回 413 并丢掉。
- 下载（`attachment/read`、`files/read`、`browser/frame`）：参数带 `offset`、`length`（`length` 不超过 4MB），回复 `{data, size}`，再加上现在放在响应头里的信息（如 `mime`、修改时间）。界面按 `size` 接着取，直到取完。

### 方法（第一期）

参数与回复的写法，下面的表不再逐个重复：
- 参数：现在 HTTP 请求体里的 JSON 字段原样放进参数。
  - 路径里的 `{id}` 叫 `threadId`；
  - 其他路径参数（`name`、`task`、`call`、`path`、`action`）和查询参数（如 `?name=`、`?workspace=`、`?conversation=`、`?task=`）同名放进参数；
  - 要知道是谁发的请求时带 `user`。
- 回复：现在的响应体 JSON 原样作为 result；现在回 204 的，回 `{}`。
- 一条回复或通知就是一行，不能超过 16MB，所以二进制一律分块。

实现第 3 块时补的细节（2026-10-24，第 5 节没写到的几处）：
- 路径里的 `{id}` 叫 `threadId` 的只有对话（`/runs/{id}/…`）。`/traces/{id}`、`/memory/{id}/forget`、`/evals/{id}`、`/evals/{id}/{action}` 里的 id 不是对话，参数名就叫 `id`，动作叫 `action`。
- 分块上传的回复：不是最后一块回 `{uploadId}`；带了不认识（或已经结束）的 `uploadId` 回 400，`offset` 与已收到的字节数不等也回 400，累计超过上限回 413 并丢掉。
- 分块下载的回复：`attachment/read` 是 `{data, size, mime}`，`files/read` 是 `{data, size, name, mime, modified}`（文件面板固定 `application/octet-stream`，`modified` 是 Unix 毫秒），`browser/frame` 是 `{data, size, mime}`。`length` 不在 1 到 4MB 之间、`offset` 小于 0 或超过 `size` 都回 400。
- `subscription` 是字符串，同一个连接里不能用两次（回 400）。
- `settings/write` 的 `layer`（与 `user`）和设置项平铺在一份参数里，不是另起一个对象：请求体里原来的设置项照旧，只多一个 `layer`。
- `thread/started` 也给评测、导入产生的对话发：它们同样在 `thread/list` 里。
- 出错时只有 `code` 与 `message`：`thread/rewind` 原来在 409 的响应体里还带一份 `result`，协议里丢掉（界面只用 `error` 的文字）。
- 一块原始数据超过 4MB 回 400（不只是累计上限）：界面按 4MB 切，超过说明不是按协议在传，base64 之后会顶到一行 16MB 的上限。
- 审计记录的 `Detail` 写 `initialize` 里的 `client.name`，看得出是哪个界面点的：网页端 `tern web`，chat `tern chat`，app-server 自己的 `start`、`stop`、`proxy` 是 `tern app-server`。

对话与轮次：

| 方法 | 参数 | 回复 | 对应的网页端接口 |
| --- | --- | --- | --- |
| `initialize` | 见上 | 见上 | — |
| `thread/list` | — | 对话摘要（同 `run.summary`） | `GET /runs` |
| `thread/start` | user、query、provider、model、workspace、permission、images | 摘要（不自动订阅，界面接着发 `thread/resume`） | `POST /runs` |
| `thread/resume` | threadId、after、subscription（界面取，同一连接内不重复） | `{seq}`：订阅那一刻对话最后一个事件的序号，界面据此分清哪些是已有的、哪些是新来的。随后用 `thread/event` 推 after 之后的全部事件，再推新事件 | `GET /runs/{id}/events` |
| `thread/unsubscribe` | subscription | — | 页面关掉事件流 |
| `turn/start` | threadId、query、permission、images、compact | 摘要 | `POST /runs/{id}/messages`（compact 是新加的，给 chat 的 `/compact`） |
| `turn/steer` | threadId、text | — | `POST /runs/{id}/steer` |
| `turn/interrupt` | threadId | 第一次优雅停止，第二次强制结束 | `POST /runs/{id}/stop` |
| `approval/respond` | threadId、kind（tool、network、question）、页面现在提交的字段 | — | `POST /runs/{id}/approval`、`/network`、`/answer` |
| `thread/rewind` | threadId、页面现在提交的字段 | 同现在 | `POST /runs/{id}/rewind` |
| `thread/import`、`thread/attribute` | 同现在 | 同现在 | `POST /runs/import`、`/runs/{id}/attribute` |

其余功能（参数与回复都同现在的接口，二进制按块）：

| 方法 | 对应的网页端接口 |
| --- | --- |
| `settings/read`、`settings/write`（layer：user、platform） | `GET /settings`、`PUT /settings`、`PUT /settings/platform` |
| `workspace/add`、`workspace/remove` | `POST`、`DELETE /settings/workspaces` |
| `model/list`、`command/list` | `GET /models`、`GET /commands` |
| `attachment/upload`、`attachment/read` | `POST /attachments`、`GET /attachments/{name}` |
| `files/list`、`files/read`、`files/write`、`files/delete`（沙箱文件面板） | `/runs/{id}/files…` |
| `browser/frame` | `GET /browser/{task}/{call}` |
| `trace/spans`、`trace/metrics` | `GET /traces/{id}`、`GET /trace-metrics` |
| `memory/list`、`memory/forget` | `GET /memory`、`POST /memory/{id}/forget` |
| `audit/list` | `GET /audit` |
| `hub/list`、`hub/enabled`、`hub/skill/create`、`hub/skill/delete`、`hub/skill/toggle`、`hub/mcp/add`、`hub/mcp/delete`、`hub/mcp/toggle`、`hub/mcp/test` | `/hub…` |
| `eval/list`、`eval/cases`、`eval/get`、`eval/start`、`eval/action` | `/evals…` |
| `server/shutdown` | 供 `tern app-server stop` 用 |

app-server 发给界面的通知：

| 通知 | 发给谁 | 内容 |
| --- | --- | --- |
| `thread/event` | 订阅它的连接 | subscription、threadId、一个 `event`；delta 也推，但照旧不写进存档。对话结束时最后一条是 `{subscription, threadId, end: true}`，见下面“订阅与断线” |
| `thread/started` | 所有连接 | 新建的对话的摘要 |
| `thread/status/changed` | 所有连接 | threadId、status：`active`（agent 在跑）或 `idle`；一轮开始、结束时各发一次 |

后两条只是让界面知道列表变了，各连接分别发，不卡 `run.add`。

**审批怎么走**：agent 发来的审批与提问照现在 `run.handle` 的做法记成 approval 事件，推给所有订阅者；任何一个界面回 `approval/respond`，先到的算数，app-server 照现在记一条 decision、answer 或 allow/deny 事件，其他界面看到它就收起按钮。这里不照 Codex 让 app-server 向界面发请求：界面可能断线，也可能同时有好几个，挂在某个连接上的请求不好收场；做成事件后，重连的界面靠 `thread/resume` 就能看到还在等的审批。

**订阅与断线**：
- 订阅就是一条 SSE 事件流搬到协议上：`thread/resume` 带上界面自己取的 subscription 编号。编号由界面取，是因为推送可能比回复先到，界面要在发请求之前就登记好。
  - 同一个连接可以有多个订阅，同一条对话也可以订阅多次（网页端开两个标签页）。
  - 连接断开即全部退订。
- 每个订阅一个 goroutine 推送，写法同现在的 `streamEvents`：
  - 等 `r.changed`，把游标之后的事件逐条用 `thread/event` 发出去。
  - 发完时对话已经结束（`r.Done`），就发 `end: true` 并结束这个订阅，同现在 SSE 的 `event: end`。之后续聊，界面再 `thread/resume`，after 是最后收到的 seq。
  - 一个连接写得慢（比如 SSH 卡住），只卡住它自己的推送，不卡 `run.add` 和别的连接。
- `protocol.Conn` 的 handle 不能阻塞，所以 app-server 收到的每个请求各起一个 goroutine 处理。`initialize` 例外：它不会阻塞，在读循环里直接处理，紧跟着发来的请求才一定排在它后面。
- 界面重连后，对每条正在看的对话发 `thread/resume {after: 最后收到的 seq}` 补齐。事件序号沿用 `run.seq`（只增不减）。
- 没有任何界面连着时对话照常跑；等审批的一直等着，同现在关掉网页的情况。
- 不做 Codex 的“没人订阅 30 分钟卸载”：现在网页端也是把所有对话放在内存里。

## 6. app-server 进程

**命令**

| 命令 | 做什么 |
| --- | --- |
| `tern app-server [-dev]` | 前台运行，日志写 stderr。`-dev` 从网页端移过来：每轮用 `go run .` 启动 agent，要求 `TERN_HOME` 是源码根目录 |
| `tern app-server start [-dev]` | 幂等：控制 socket 能答 `initialize` 就直接返回（已在运行的不会因为 `-dev` 换模式）；否则以新会话（setsid）在后台起 `tern app-server`，stdout、stderr 写进 `.data/app-server/app-server.log`，等 socket 能答 `initialize` 再返回（最多 30 秒）；子进程提前退出（锁被占住、监听失败）就不再等，报错并带上日志的最后几行。输出一行 JSON：socket、pid、version |
| `tern app-server stop` | 发 `server/shutdown` 并等它退出：按 pid 文件里的进程号等进程真的不在（`kill(pid, 0)` 返回 ESRCH），不等 socket 被删掉——删掉 socket 之后进程还要收尾，文件锁这时还没放开，紧接着的 start 会起不来。正在跑的 agent 不停，同现在网页端重启：之后由新的 app-server 接管 |
| `tern app-server proxy` | 先做 start，再在 stdin/stdout 与控制 socket 之间原样转发，任何一边断开就退出。经 SSH 时由界面调用 |

前台运行的 `tern app-server` 在 `internal/appserver`。`start`、`stop`、`proxy` 都是连 app-server 的一方，放在 `internal/appclient`：网页端和 chat 不带 `-remote` 时也调同一个 start。协议版本常量在 `internal/appserver`，`appclient` 引用它；`appserver` 不引用 `appclient`。

同一个 `TERN_HOME` 只能有一个 app-server：
- 用 `.data/app-server/lock` 加文件锁（`syscall.Flock`，`LOCK_EX|LOCK_NB`），pid 记在 `.data/app-server/app-server.pid`。
- 已被锁住时报“这个 TERN_HOME 已经有 app-server 在运行（pid N）”，然后退出。
- 拿到锁之后，先删掉上次留下的 `control.sock`，再监听。
- `stop` 时如果没有在运行的，输出“没有在运行的 app-server”，正常退出。
- 收到 `server/shutdown`：先回复，再关监听和所有连接，删掉 socket 与 pid 文件后退出。agent 进程不动。

**从 `internal/web` 搬进来的**（接口外形从 HTTP 处理函数改成协议方法，逻辑不改，只去掉租户相关的部分）：
- 对话：列表、存档、事件（`loadRuns`、`run.add`、`createRunFile`）。
- 启动与接管 agent：`launch`、`beginTurn`、`connect`、`adopt`、`recoverJournal`、`finish`、`cleanLive`、`agentCommand`。
- 续聊：`continueRun` 里的逻辑、`resumeHistory`、`checkpointHistory`。
- 停止、插话、审批记账：`stopRun`、`steerRun`、`run.handle` 与 `toolWaits`、`approvals`、`asks`。
- 撤销：`rewindRun`。
- 模型代理：`proxy`、`relayStream`、`archive.go`。
- 设置、工作区、附件：`settings.go`、`attachments.go`、`listModels`、`listCommands`。
- 沙箱文件面板、浏览器画面：`files.go`、`browserFrame`。
- 调用链、记忆、审计：`traceSpans`、`traceMetrics`、`readSpans`、`ownsTrace`、`listMemory`、`forgetMemory`、`listAudit`。
- Skills/MCP 中心：`hub.go`。
- 评测、导入、归因：`eval.go`（含 `attributeRun`）、`import.go`。

**模型代理改走 Unix socket**：`.data/app-server/llm.sock`，上面跑 HTTP，路由同现在的 `/llm/<对话ID>`。改的原因有三个：
- app-server 没有固定端口：网页端的 `-addr` 属于界面。
- 同一台机器上可以有好几个 `TERN_HOME`。
- socket 目录 0700，只有本人的进程连得上。

agent 这一侧要改的：
- 环境变量改成 `AGENT_PROXY_SOCKET=.data/app-server/llm.sock`（相对路径，agent 的工作目录就是 `TERN_HOME`）和 `AGENT_PROXY_URL=http://tern/llm/<对话ID>`。
- 有 `AGENT_PROXY_SOCKET` 时经它拨号，并按“经代理”处理。现在这个判断看的是地址是不是 `http://127.0.0.1`（`internal/llm/llm.go:98`）。
- 连不上时，ENOENT 和 ECONNREFUSED 都当作 app-server 在重启，最多等 `protocol.DetachGrace`。现在只认 ECONNREFUSED（`llm.go:164`）。

app-server 拿到文件锁后，先删掉旧的 `llm.sock` 再监听，同 `control.sock`；收尾时也删掉。

**起 agent 用哪个程序**：
- app-server 起 agent，以及 `-skills-list`、`-mcp-list` 这类子进程，用 `/proc/self/exe`；没有 `/proc` 时用 `os.Executable()`。`-dev` 照旧用 `go run .`。
- 原因：`go run . web` 拉起的 app-server 跑的是 go 的临时编译文件。网页端一退出，这个文件就被删掉，按路径起不了 agent；`/proc/self/exe` 指向的是正在运行的程序本身。
- 所以重启网页端不会换掉 app-server 的代码。改了代码要先 `tern app-server stop`，再起界面；或者用 `-dev`。README 写明这一点。

## 7. 界面

### tern web

- 用法：`tern web [-addr …] [-remote <ssh 别名>]`。不带 `-remote` 时连本机 app-server，先做一次 start；带 `-remote` 时经 SSH 连（第 9 节）。
- 网页端去掉 `-dev`：启动 agent 的是 app-server，要从源码跑就 `go run . app-server -dev` 或 `go run . app-server start -dev`。
- 启动时打印连上的 app-server 的 `host`、`home` 与 `version`（`initialize` 的回复），并提示 agent 由 app-server 启动。`tern web` 只接受 `-addr` 与 `-add-user`。
- 路由和页面不变，每个接口改成调对应的协议方法，所有请求共用一个连接。事件流 `GET /runs/{id}/events` 改成先 `thread/resume`，再把 `thread/event` 转成 SSE。
- 协议错误的 `code` 在 400–599 之间的，原样作为 HTTP 状态和文字返回；其余的（连接断了、协议本身的错误）回 502，文字是错误信息。
- 第 5 块做重连之前：连接断了（比如执行了 `tern app-server stop`），下一个请求先 start 再重新连。正在推送的事件流直接结束，页面照现在的样子提示“连接中断”。
- 本机模式和远程模式走同一套代码，区别只在连接怎么建立。
- 登录照旧，用本机的 `.data/users.json`。
- 页面只加一处：顶栏显示主机（本机或 devbox）和连接状态（已连接、重连中）。
  - 网页端新增 `GET /connection`（SSE）：连上时先推一次当前状态，之后变了再推。
  - 内容是 `{host, remote, state, runs}`：
    - `host`：本机模式是 `initialize` 回复里的 host，远程模式是 ssh 别名；
    - `state`：`connected` 或 `reconnecting`；
    - `runs`：收到 `thread/started` 或 `thread/status/changed` 就加一。
  - 页面在顶栏 `#status` 前面加一个 `#host`，显示“主机 · 状态”，本机模式写“本机”，重连中换醒目的样式。
  - `runs` 变了就刷新对话列表（`loadRuns`）。这样 chat 开的对话会实时出现在网页端的列表里（第 13 节第 3 条）。
- 重连（第 5 块做，取代第 3 块的“下一个请求先 start 再重连”）：
  - 本机模式和远程模式一样：连接断了，`appclient` 按 1、2、4…最多 30 秒退避重连；本机模式每次先 start，远程模式重新起 ssh。
  - 重连期间所有请求立刻失败，网页端回 503“正在重连 <host>”。断在半路的那个请求不重发，照实报错。
  - 正在推送的订阅不结束：重连后 `appclient` 在新连接上对它们重新 `thread/resume`，after 是最后收到的 seq。页面的 SSE 一直开着，只是中间停了一会儿。
  - 启动时第一次就连不上，直接报错退出（带上 ssh 的报错），不进入重连。
- `/mcp`（内置的计算器 MCP，`mcp.Handler`）留在网页端，不动。

### tern chat

- 改成 app-server 的客户端：`tern chat [-remote <别名>] [-continue <对话ID>] [-provider …] [-model …] [-permission-mode …] [-workspace <名字>]`。
- 不再自己起 agent（现在在 `internal/chat/chat.go:361`），也不再把任意 agent 参数原样传下去：别的参数报错，说明工具来自设置。工具来自 app-server 的设置，和网页端一样。去掉 `-tenant`。请求里不带 `user`。
- 对话就是 app-server 的对话：网页端能看到 chat 开的对话；chat 用 `-continue` 或 `/resume <对话ID>` 也能接着网页端的对话聊。
- 一轮怎么跑：
  - 新对话 `thread/start`，续聊 `turn/start`，然后用 `appclient` 的 `Subscribe` 跟着这一轮，读到 `end` 为止。
  - 续聊时 `after` 填上一轮最后收到的 seq，只显示新的事件。
  - `/resume` 或 `-continue` 接一条已有的对话：先 `Subscribe(对话ID, 0)`。seq 不超过回复里 `seq` 的是已有事件，不显示，只用来算轮次、最后的 seq、还没人答的审批。超过的照常显示（对话正在跑时就是接着看）。接上后打印这条对话的第一个问题和最后一轮回答的开头。
- 显示照旧用现在的 `view`，事件换算回原来的协议消息：
  - `notify`、`delta` 事件：`Method` 和 `Body` 就是原来那条通知，交给 `view.show`；
  - `approval` 事件：同原来 agent 发来的请求，交给 `view.prompt`。回答走 `approval/respond`：`tool/requestApproval` 是 tool，`network/requestApproval` 是 network，`user/requestInput` 是 question。审计由 app-server 写，chat 不再自己写；
  - 别的界面先答了：看到 `decision`、`answer`（`Text` 是调用ID）或 `allow`、`deny`（`Text` 是域名），就不再等这一问；
  - `stdout` 事件是 agent 的终端日志，写进 `.data/chat.log`，不显示；
  - `turn/completed` 里没用上的插话（`queued`）照旧接着发。
- 命令：
  - `/turns` 从这条对话的事件算，不加协议方法：
    - 每个 `start`、`continue` 事件算一轮，问题是它的 `Text`；
    - 检查点ID取这一轮 `turn/started` 通知的 `turn_id`，结局取这一轮的 `exit`。
  - `/rewind <n>`：先 `thread/rewind {turn: 第 n 轮的检查点ID, dry_run: true}`，列出会恢复、删除哪些文件以及冲突。没有冲突就问 y/n，答 y 再真正撤销。撤销对文件和对话都生效，同网页端。原来的 `files`、`chat` 两种只退一边的方式，第二期和 `/fork` 一起做。
  - `/mode` 设之后各轮的 `permission`。`/approve` 切到 edit，再发一轮“计划已批准”。`/compact` 让下一轮的 `turn/start` 带 `compact: true`，app-server 原样交给 agent 的 `turn/start`。
  - `/new` 让下一句开新对话；`/resume <对话ID>` 见上；`/exit` 退出。
  - `/fork` 第二期再做，因为协议还没有 `thread/fork`。输入 `/fork` 时提示这一点。
- 运行中直接输入的话是插话（`turn/steer`）；`turn/steer` 失败（这一轮刚结束）就留到下一轮发，同现在。
- Ctrl+C：第一次 `turn/interrupt`，再按一次还是 `turn/interrupt`（app-server 第二次就强制结束）。没有在跑的一轮时，Ctrl+C 退出 chat。
- 退出 chat 不停对话，对话在 app-server 里接着跑。退出时如果还有一轮在跑，提示一句“还在跑，可以 `tern chat -continue <对话ID>` 或在网页端接着看”。
- 标准输入结束（比如 `printf … | tern chat`）时，等正在跑的这一轮结束再退出，不再中断它。
- 断线重连时（第 5 块），chat 打一行灰字“连接断开，重连中…”，连上后打“已重连”，这一轮的显示接着往下走。
### 桌面端（以后）

不在这份设计里。约束只有一条：界面连 app-server 的代码放在 `internal/appclient` 包里，内容包括本机 socket 或 SSH proxy、`initialize`、重连、订阅补齐。网页端和 chat 都用它，桌面端以后直接复用。

`appclient` 给界面的接口：
- `Call(ctx, method, params, result)`。
- `Subscribe(threadId, after)` 返回一个订阅：界面从它依次取事件，直到 `end`；`Close` 就退订。订阅上带着 `thread/resume` 回复里的 `seq`。
  - 收到的 `thread/event` 先放进这个订阅自己的队列（不限长）。界面读得慢，不会卡住同一连接上的其他调用（`protocol.Conn` 的 handle 不能阻塞）。
  - 第 5 块的重连由 `appclient` 按每个订阅最后收到的 seq 重新 `thread/resume`，界面不用管。
- `thread/started`、`thread/status/changed` 交给界面注册的回调。

实现第 5 块时补的细节（2026-10-24，接上 `internal/appclient` 的实际接口）：

- `Dial(name)` 连本机，`DialRemote(name, alias)` 经 ssh 连远端；两者都先完成 `initialize`，连不上就直接返回错误（远程时带上 ssh 的 stderr 最后几行），不进入重连。`ValidAlias(alias)` 是 `-remote` 的校验（`web`、`chat` 用）。
- 连接状态由 `appclient` 给：`Host()` 是顶栏显示的主机（远程是 ssh 别名，本机是 `initialize` 回复里的 host），`State()` / `OnState(func(State))` 报 `connected`、`reconnecting`。重连期间 `Call`、`Subscribe` 立刻返回带 `ErrReconnecting` 的错误，网页端据此回 503“正在重连 <host>”；`Subscribe` 上已有的订阅不受影响。
- 重连后某个订阅重新 `thread/resume` 失败：只有协议错误码是 404（这条对话已经不在了）才结束这个订阅，界面那边取到 `end`；别的失败（多半是连接又断了）留着它，下一次重连再按最后收到的 seq 补齐。

## 8. 去掉租户账号

这一版只有平台空间。以后要再做租户，另写设计。

- 网页端删掉租户相关的部分：
  - 租户策略：`policy`、`allows`、`allowsTool`。
  - 同时运行数限额：`underQuota`。
  - 按租户过滤与核对：`runOf`、`listRuns`、`ownsTrace` 里的租户比较。
  - 只给平台账号用的包装：`platform`。现在所有账号都是平台账号，这层就不需要了。
- `-add-user` 去掉 `-tenant`、`-features`、`-max-running` 三个参数。
- `.data/users.json` 里带 tenant 的账号登录时拒绝，提示“租户账号暂不支持”；`tenants` 字段读到了也不用。
- `.data/tenants/` 下已有的数据不动、不删。启动时只读 `.data/runs`，不再读 `.data/tenants/*/runs`（`loadRuns`）。
- 页面和接口里跟租户有关的字段一起去掉：
  - `/me` 只回 `name`；
  - 设置接口不再回 `can_edit_platform`；
  - 页面删掉按租户和功能隐藏入口的分支（`main.js` 的 `loadMe`），以及设置页里只给平台账号的判断（`pages.js`），一律按平台账号显示。
- 审计记录里的 `Tenant` 字段不再填（`audit.Record` 本身不改）。
- `internal/tenant` 和 agent 的 `-tenant` 参数不动：`queue` 和命令行还在用它们。app-server 启动 agent 时不传 `-tenant`。
- 同步改 README 里的账号说明，以及 `internal/web/AGENTS.md` 里“平台账号与租户账号两种”“租户须明确开通 `workspace`”这类约束。

## 9. SSH 怎么连

- 用系统的 `ssh`（OpenSSH），读 `~/.ssh/config`。只接受具体的 Host 别名或 `user@host`，带通配符的 Host 不支持（同 Codex）。
- 别名要求：不以 `-` 开头，只含字母、数字和 `._@-`，免得被 ssh 当成选项；含 `*`、`?` 的直接拒绝。
- 命令：`ssh -T -o BatchMode=yes <别名> 'exec "$SHELL" -lc "tern app-server proxy"'`。走登录 shell，`~/.profile` 里设的 PATH 和 `TERN_HOME` 才会生效（Codex 也要求 codex 在登录 shell 的 PATH 上）。
- `BatchMode=yes`：不在后台卡在输密码或确认主机指纹那一步；第一次连某台机器，要先在终端里手动 `ssh` 一次。ssh 的报错原样显示在终端或页面上。
- ssh 的 stderr 留最后几行。ssh 退出或连不上时，报错里带上这几行（比如 `Permission denied`、`command not found`）。
- `-remote` 时，`appclient` 就用 ssh 进程的标准输入输出做协议连接，`initialize` 和本机模式一样。界面名字照旧：`tern web`、`tern chat`。
- 远端要准备好的：
  - 装好 tern，协议版本和本机一致。
  - `TERN_HOME` 里有 `.env`（模型密钥），设置里登记了工作区。
  - 工作区在网页端设置页里登记，改的就是远端的设置。

## 10. 按文件的改动

| 位置 | 改动 |
| --- | --- |
| `main.go` | `TERN_HOME` 与 chdir；新子命令 `app-server`（含 `start`、`stop`、`proxy`）；`-app-server` 改名为 `-agent-server` |
| `internal/appserver`（新） | 控制 socket 与多连接、`initialize`、订阅与推送、分块传输、第 5 节的方法；从 `internal/web` 搬来的部分（第 6 节）；`AGENTS.md` 写这个包的约束 |
| `internal/appclient`（新） | 界面这一侧：`start`、`stop`、`proxy`；连本机 socket 或起 ssh proxy、`initialize`、断线重连、`thread/resume` 补齐 |
| `internal/web` | 搬走第 6 节列的部分；去掉租户（第 8 节）；剩下登录、页面、HTTP 到协议的翻译、`-remote`、顶栏状态 |
| `internal/web/page` | 顶栏的主机与连接状态 |
| `internal/chat` | 改成 app-server 的客户端（第 7 节） |
| `internal/agent/child.go` | 子 agent 用 `-agent-server` 启动 |
| `internal/llm` | `AGENT_PROXY_SOCKET` 拨号；代理模式的判断；连不上时的判断 |
| `internal/protocol` | 不改：多连接的接受循环放在 `internal/appserver`，每个连接用 `protocol.NewConn` |
| `README.md` | `TERN_HOME`；`app-server` 的几个命令；`-remote`；`-agent-server`；chat 用法的变化；去掉租户账号的说明 |
| `AGENTS.md`、`internal/web/AGENTS.md`、`internal/protocol/AGENTS.md` | 对话归 app-server；网页端和 chat 是界面；代理改走 socket；参数改名；去掉租户 |

不要改的：
- agent 本身，除了 `internal/llm` 的代理拨号和参数改名。
- 检查点格式、存档格式、沙箱、权限。
- 页面，除了顶栏和去掉租户的分支（第 8 节）。
- `internal/tenant`。
- `tern -question`、`queue`、`eval` 命令行：仍然直接起 agent，不经 app-server。

## 11. 分期

- **第一步**（可以单独合并）：`TERN_HOME`；`-app-server` 改名为 `-agent-server`。已实现（2026-10-23）。
- **第一期**：
  - app-server 与协议。
  - 网页端的全部功能搬进 app-server，网页端和 chat 改成客户端，去掉租户账号。
  - SSH 远程。本机模式和远程模式功能一样。

  交给 tern 时分五块，按顺序做。每块做完都能编译运行，review、验收后再做下一块。五块都已实现（2026-10-24）：
  1. app-server 进程：`tern app-server` 与 `start`、`stop`、`proxy`；控制 socket、文件锁、`initialize`、`server/shutdown`。`internal/appclient` 先只有本机连接和 `initialize`。网页端不动。
  2. 去掉租户账号（第 8 节）：只改网页端，代码还没搬。先删掉租户这一层，第 3 块要搬的代码就少一层判断。
  3. 网页端读写数据的部分全部搬进 app-server（第 6 节列的全部）：
     - 模型代理改走 `llm.sock`；二进制分块；通知；`-dev` 移到 app-server。
     - 网页端的接口全部改成调协议，做完后网页端只读 `.data/users.json`。
     - 这一块不再拆开：文件面板要用对话的数据，Skills 中心和启动 agent 共用 `.data/hub.json` 的读取，分两次搬就要写两遍临时代码。
  4. chat 改成客户端（第 7 节）。
  5. SSH：`appclient` 起 ssh proxy、断线重连与补齐；网页端和 chat 加 `-remote`；顶栏显示主机与连接状态。
- **第二期**：`thread/fork` 与 chat 的 `/fork`；一个网页端同时连几台主机（页面读 `~/.ssh/config` 列出主机，对话列表按主机分组）。
- **第三期**：桌面端。

## 12. 这一期不做

- WebSocket、TLS、令牌鉴权：只走 Unix socket 和 SSH。
- 在远端自动安装或升级 tern：Codex 桌面端也要求远端先装好。
- Codex 的其余方法：item 事件格式、`thread/fork`、归档、删除、命名、目标、`thread/compact/start`、`thread/shellCommand`、`command/exec`、`fs/*`、配置文件读写。
- 没人订阅时卸载对话。
- 远程模式下网页端内置的计算器 MCP（`/mcp`）：它在本机的网页端进程里，远端的 agent 连不到。要用就在远端另起一个网页端，或者第二期再说。
- 手机。
- 租户账号（第 8 节）。
- chat 按检查点ID续聊（现在的 `-continue <检查点ID>`、`/resume <检查点ID>`）：改成按对话ID，以前 chat 留下的检查点不能再接着聊。

## 13. 验收

用真实模型，在临时 clone 里做编码任务。devbox 可以就是同一台机器：`ssh localhost`，远端用另一个 `TERN_HOME`。

1. **本机模式不退化**：`tern web` 下新对话、续聊、停止、插话、审批、提问、撤销、附图、文件面板、浏览器画面、调用链、记忆、审计、Skills/MCP 中心、评测、导入、归因都照常。
2. **远程模式功能齐全**：`tern web -remote devbox` 下把第 1 条再走一遍。附一张接近 20MB 的图片、在文件面板上传下载接近 10MB 的文件，验证分块传输。
3. **终端和网页同看**：`tern chat` 开一条对话，网页端的列表里实时出现，打开能看到过程；在网页上点审批，chat 那边接着往下跑。
4. **SSH 断了任务继续**：`tern web -remote devbox` 发起一个要跑几分钟的任务，然后杀掉本机的 ssh 进程，或者断网 60 秒。页面显示“重连中”；恢复后补齐中间的过程，任务正常结束；devbox 上的 `tern app-server` 和 agent 一直在跑。
5. **关掉所有界面再回来**：任务进行中关掉网页端和 chat，10 分钟后重新连上，对话已经结束，过程完整。
6. **app-server 重启**：任务进行中执行 `tern app-server stop`，再 `tern app-server start`。agent 被新的 app-server 接管，模型请求等它回来后继续。
7. **版本不一致**：远端装一个协议版本不同的 tern，`initialize` 报出两边的版本。
8. **密钥只在 devbox**：本机的 `.env` 是空的，带 `-remote` 时对话照常。
9. **租户账号**：带 tenant 的账号登录被拒，提示“租户账号暂不支持”；`.data/tenants/` 下的文件没有变化。

## 14. 已答复的问题（2026-10-23）

1. 命名：常驻进程叫 `tern app-server`，agent 的 `-app-server` 改名为 `-agent-server`。
2. `TERN_HOME` 默认值：在源码根目录里就用当前目录，否则用 `~/.tern`。
3. 模型代理改走 Unix socket（`AGENT_PROXY_SOCKET`）。
4. 先去掉租户账号（第 8 节）。
5. chat 不再透传 agent 参数，工具来自设置；退出 chat 不停对话；不再按检查点ID续聊。
6. 审批用“事件加 `approval/respond`”，不用 app-server 向界面发请求。
7. 文件面板、浏览器画面、调用链、记忆、审计、Skills/MCP 中心放进第一期：搬法和其他接口一样，留在网页端反而多一条临时路径；二进制内容分块传。
