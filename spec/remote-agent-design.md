# 远程 agent：网页操作用户本机的代码

状态：设计稿，待确认。

## 1. 目标与范围

用户在自己的机器上运行 `tern connect`，主动连上我们部署的网页端；之后在网页上发起对话、选他本机登记过的目录，任务由**他本机的 tern** 执行：读写文件、跑命令都发生在他的机器上，网页只负责显示过程和让人审批。思路同 Claude Code 的 Remote Control。

要做到：

- 用户本机不开任何端口，只有一条向外的连接；断线重连后，正在跑的对话接着显示。
- 模型密钥只在服务器上，用户不需要自己的密钥。
- 服务器能做的事有本机设定的上限：只能动用户登记的目录，权限模式有上限，bash 一律在沙箱里，服务器下发的 hooks、MCP 命令不在本机执行。
- 现有的对话、续聊、停止、插话、审批、提问流程不变，页面上远程对话和本地对话看起来一样。

第一期不做：macOS 与 Windows 上的 bash（文件工具可用）、远程对话的撤销与沙箱文件面板、浏览器与搜索工具、长期记忆、知识库检索、评测（评测仍只在服务器上跑）。见第 9 节。

### tern 的几种界面

agent 只有一个；下面这些是它的界面，都经同一套 app-server 协议与它通信。

| 中文 | 命令或形态 | 是什么 |
| --- | --- | --- |
| 命令行 | `tern` | 一次一个任务，适合脚本 |
| 终端 | `tern chat` | 终端里的多轮对话 |
| 网页端 | `tern web` | 浏览器里用，可以部署给别人（以前叫观测台） |
| 桌面端 | 单独的应用（以后） | 装在本机，窗口里用 |

连接器 `tern connect` 不是界面：它把本机目录接到网页端，在本机执行任务，就是本设计要做的东西。

桌面端以后做时和本设计的关系：桌面端本来就跑在用户机器上，只用本机时它就是本地的网页端；连上服务器时，它同时承担连接器的角色。所以连接器的逻辑放在 `internal/connect` 包里，`tern connect` 只是一个薄的命令行入口，桌面端以后直接复用这个包。网页端的页面也会被桌面端复用，页面里不要写死“一定部署在服务器上”这类假设（比如登录这一步）。

## 2. 用户怎么用

```text
1. 网页：设置 → 本机连接 → 新建连接，起个名字（如“笔记本”），拿到一次性显示的令牌
2. 本机：tern connect -server https://tern.example.com -token <令牌> -dir ~/code/app -dir ~/code/lib
         终端显示：已连接 · 账号 alice · 目录 app、lib · 权限上限 edit
3. 网页：新对话 → 工作区下拉里出现“笔记本：app” → 提问
4. 本机终端：逐行显示这条对话在做什么（读了哪些文件、跑了什么命令）；Ctrl-C 停止全部对话并断开
5. 网页：设置 → 本机连接 → 吊销，令牌立即失效，连接断开
```

`-dir` 可以写多次；目录名默认取最后一段，重名时用 `-dir app=~/code/app` 指定。只有这里列出的目录会出现在网页上。

## 3. 架构

```text
 用户本机                                                服务器
┌──────────────────────────────────────────┐            ┌─────────────────────────────────────────┐
│ tern connect（连接器，长期运行）         │  wss 一条  │ tern web（网页端）                      │
│   · 登记目录、权限上限                   │◀──────────▶│   · /connect：认令牌，按账号登记连接器  │
│   · 每条对话起一个 agent 子进程          │  向外连接  │   · 远程对话的 run.conn 走这条连接      │
│   · 在本机 Unix socket 与 wss 之间转发   │            │   · 页面、存档、审批照旧                │
│         │ AGENT_APP_SOCKET               │            │   · /llm/<对话>：模型代理，加密钥       │
│ tern -app-server -workspace ~/code/app   │── https ──▶│        │                                │
│   · 文件工具、bash 沙箱、子 agent 在本机 │  模型请求  │        ▼ 方舟 / DeepSeek / Coconut      │
└──────────────────────────────────────────┘            └─────────────────────────────────────────┘
```

两条连接：

- **控制连接**（连接器 ⇄ 网页端，WebSocket）：启动、停止对话，以及把每条对话的 app-server 协议消息原样来回转发。
- **模型请求**（agent → 网页端 `/llm/<对话ID>`，HTTPS）：和现在本机 agent 经网页端代理一样，只是地址从 `http://127.0.0.1:8090` 换成服务器的 https 地址。

agent 本身几乎不改：它照旧以 `-app-server` 在 `AGENT_APP_SOCKET` 上等界面连进来。连接器在本机扮演现在网页端的角色（启动进程、Dial、`session/attach`），网页端那一侧把每条远程对话当成一个普通的 `protocol.Conn`，底下换成 WebSocket 上的一路虚拟流。

## 4. 配对与鉴权

**连接令牌**

- 在网页“本机连接”里新建：32 字节随机数，只显示一次；服务器只存它的 SHA-256，放 `.data/connectors.json`（0600，不提交），连同账号、租户、名字、创建时间、最近一次连接时间。
- 一个令牌对应一个连接器、属于一个账号。吊销即删除记录，正连着的立即断开。
- 租户账号要租户开通新功能 `connect` 才能建连接（同 `workspace`）；平台账号默认可以。

**连接**

- 连接器向 `wss://<服务器>/connect` 发起 WebSocket 连接，`Authorization: Bearer <令牌>`；只接受 https/wss，令牌不经明文。
- 第一条消息是 `connector/hello`（见第 5 节），服务器核对后回 `connector/welcome`。同一令牌再连一次，旧连接作废（同 `session/attach` 的做法）。
- 网页端只把**这个账号**发起的对话派给它，按编号访问的接口照旧核对租户。

**账号与连接器**

- 加了连接器之后，网页端仍然要登录。账号密码管的是“谁在用网页”，连接令牌管的是“哪台机器替这个账号干活”，两者不互相替代。令牌只能由登录后的账号在“本机连接”里创建，归这个账号所有；连接器不知道账号的密码。
- 一个连接器只属于一个账号。同一台机器要给两个账号用，就跑两个连接器，各用各的令牌和数据目录：

  ```sh
  TERN_HOME=~/.tern-alice tern connect -server https://tern.example.com -token <alice 的令牌> -dir ~/code/app
  TERN_HOME=~/.tern-bob   tern connect -server https://tern.example.com -token <bob 的令牌>   -dir ~/code/lib
  ```

  两边的检查点、快照、审计、沙箱临时目录互不相干，网页上各自只看到自己登记的目录。
- 两个账号可以登记同一个目录，但同时改同一份代码时看不到对方的改动：编辑前“读过的内容没变”的检查能拦下直接覆盖，拦不住逻辑上的冲突。不同账号最好用不同的目录或 git worktree。
- 以后要一个进程服务多个账号，可以让 `tern connect` 接受多个 `-token`，每个令牌一个独立的数据目录；第一期不做。

**断线重连**

- 连接器按 1、2、4…最多 60 秒退避重连；agent 子进程不受影响：它们在本机 socket 上脱离，最多等 `protocol.DetachGrace`（10 分钟），消息按序号留在内存里。
- 重新连上后，网页端对这个连接器上每条还没结束的对话发 `run/attach {after}`，连接器重新 Dial 本机 socket、带着 after 做 `session/attach`，补发的消息经 WebSocket 回到网页端。这和网页端重启后接管本机 agent（`adopt`）是同一套机制。
- 心跳：双方每 30 秒发一次 `ping`，90 秒没收到就当断开。

## 5. 控制连接上的消息

一行一条 JSON-RPC 2.0，和 app-server 协议同一种写法（复用 `protocol.Conn`）。

| 方向 | 消息 | 内容 |
| --- | --- | --- |
| 连接器 → 网页端 | `connector/hello`（请求） | 版本、系统与架构、登记的目录（名字、git 分支，不含本机路径）、可用能力（bash 是否可用）、权限上限、正在运行的对话ID |
| 网页端 → 连接器 | `run/launch`（请求） | 对话ID、目录名、agent 参数（第 6 节过滤）、模型代理地址与令牌、`turn/start` 的内容；回复 pid 或拒绝原因 |
| 网页端 → 连接器 | `run/attach`（请求） | 对话ID、after：重连后补齐 |
| 双向 | `run/data`（通知） | 对话ID 加一行 app-server 协议消息，原样转发，不解析 |
| 网页端 → 连接器 | `run/stop`（请求） | 对话ID、force：false 时由连接器向 agent 发 `turn/interrupt`，true 时按进程组结束 |
| 连接器 → 网页端 | `run/exit`（通知） | 对话ID、退出状态（连接器看得到真实的退出码） |
| 双向 | `ping` | 心跳 |

`run/data` 里是完整的协议行，所以审批（`network/requestApproval`、`tool/requestApproval`）、提问（`user/requestInput`）、插话（`turn/steer`）、停止（`turn/interrupt`）、`process/exit` 都不需要新消息。

## 6. 一次远程对话怎么跑

**发起**

1. 页面 `POST /runs`，工作区选的是“笔记本：app”。网页端照常拍设置快照、建存档，然后不调 `launch`，改调 `launchRemote`。
2. 网页端过滤 agent 参数，只保留：`-provider`、`-model`、`-permission-mode`、`-allow/-ask/-deny`、`-bash`、`-net-allow`、`-subagents`、`-ask-user`、`-max-steps`、`-context-window`、`-tool-output-tokens`、`-disallowed-tools`。去掉 `-hooks`、`-mcp-server`、`-skills`/`-skill`、`-tenant`、`-workspace`（路径由连接器按目录名补）、`-browser`、`-ds-search`、`-rag`、`-memory`（第一期）。
3. 连接器收到 `run/launch` 后**再按本机上限过滤一遍**（第 8 节），补上 `-workspace <本机路径>`，像现在的网页端一样启动 `tern -app-server …`：环境变量 `AGENT_PROXY_URL=https://<服务器>/llm/<对话ID>`、`AGENT_PROXY_TOKEN`、`AGENT_APP_SOCKET=<本机>/live/<对话ID>.sock`；工作目录是连接器的数据目录（默认 `~/.tern`），检查点、快照、会话记录、审计、沙箱临时目录都落在那里。
4. 连接器 Dial、`session/attach`，把 `turn/start` 转给 agent；之后 agent 发出的每一行包成 `run/data` 发给网页端。网页端这一侧的 `run.conn` 读到的就是这些行，后面的处理（存档、推给页面、审批）完全不变。

**审批、提问、插话**：页面点按钮 → 网页端经 `run.conn` 回复 → `run/data` → 连接器写进本机 socket → agent 继续。和本地对话一样，人的点击先写网页端的审计。

**停止**：第一次按停止，网页端经协议发 `turn/interrupt`（不变）；再按一次强制结束，本地对话是向进程组发 SIGKILL，远程对话改发 `run/stop {force: true}`。

**结束**：agent 退出，连接器发 `run/exit`；网页端记 exit、收尾，和本地对话相同。

**续聊**：网页端照旧从自己的存档还原上一轮的消息（模型请求都经过它的代理，存档里有完整的请求与回答），放进新一轮的 `turn/start`。中途被停下的对话现在是读检查点续聊，远程对话的检查点在用户本机：第一期也用存档还原（agent 的 `restoreContext` 会给没有结果的工具调用补上“结果未知”），第二期加 `run/checkpoint` 从连接器取。

## 7. 模型请求

- agent 的模型请求发往 `https://<服务器>/llm/<对话ID>`，带 `X-Agent-Token`。现在 agent 只在地址是 `http://127.0.0.1` 时才进入“经网页端代理”的模式（发 `X-Agent-*` 头、不在本地换协议）；改为**有 `AGENT_PROXY_TOKEN` 就是代理模式**，与地址无关。
- **密钥由服务器加**。现在的代理转发 agent 带来的 `Authorization`，转发到 agent 在 `X-Agent-Upstream` 里声明的地址；这对远程 agent 不成立：用户机器上没有我们的密钥，也不能让它指定任意上游（那样它能把我们加上的密钥发到自己的地址）。远程对话的请求改为：agent 只声明供应商名（`X-Agent-Provider`），网页端按 `llm.Providers` 找地址、从服务器的 `.env` 取密钥，忽略 `X-Agent-Upstream` 与请求里的 `Authorization`。
- 代理模式下 `llm.LoadConfig` 不再要求本机有密钥。
- 用量照旧记在网页端的存档里，`underQuota` 照旧按账号限制同时运行的对话数。

## 8. 安全边界

**信任模型**：连上就意味着用户允许这台服务器在他登记的目录里运行 agent。下面这些上限由连接器在本机执行，服务器（包括被攻破的服务器）改不了：

- 只有 `-dir` 登记的目录能被选作工作区；服务器只能传目录名，路径由连接器补。文件工具经 `os.Root` 限制在目录内（现有）。
- 权限模式有上限：`tern connect -max-permission edit`（默认 edit）。服务器要求 full 时，连接器降到上限；要允许 full，用户得在本机显式加 `-max-permission full`。
- bash 一律在沙箱里（现有：bubblewrap、seccomp、cgroup、断网、`.git` 只读、`.env` 换空）。本机没有 bubblewrap 时连接器不提供 bash。
- 服务器下发的 hooks、MCP stdio 命令都会在用户机器上、沙箱外执行，所以连接器一律丢掉，不管服务器怎么配。用户要在本机用 hooks，用 `tern connect -hooks <本机文件>`（第二期）。
- 联网白名单：服务器传来的 `-net-allow` 照用（来自这个账号自己的设置与审批）；连接器另有 `-net-allow-max`，给了就只接受其中的域名。

**剩下的风险**：审批按钮在网页上，服务器理论上可以替用户“点”允许。在意这一点的用户可以加 `tern connect -approve-locally`：所有审批与联网请求改在连接器的终端里由人确认，网页上的答复不算数（第二期）。

**数据去向**（要在“本机连接”页面上写明）：

- 代码留在用户机器上；模型读到的文件内容会经服务器的代理发给模型供应商，并记进服务器的对话存档（页面显示、续聊、归因都靠它）。
- 检查点、快照、会话记录、本机审计、沙箱临时文件都在用户本机的 `~/.tern/.data/`。
- 服务器的审计记人的点击；agent 的执行审计在本机。

## 9. 功能对照

| 功能 | 远程对话 | 说明 |
| --- | --- | --- |
| 对话、续聊、停止、插话、审批、提问 | 第一期 | 协议原样转发 |
| 文件工具（读、找、搜、改） | 第一期 | 本机执行，各系统都可用 |
| bash 沙箱、后台命令 | 第一期（Linux） | macOS 第三期 |
| 子 agent | 第一期 | 在本机起子进程，模型请求同样经代理 |
| 编辑文件的快照 | 第一期 | 快照存在本机，照常写 |
| 撤销 | 第二期 | `run/rewind` 交给连接器在本机执行 `tern rewind` |
| 沙箱文件面板（预览、上传、下载） | 第二期 | `run/files` 经连接器读写，限制在本对话的 /scratch |
| 附图 | 第二期 | 启动时把附件随 `run/launch` 发给连接器，写进本机附件目录 |
| 中断后从检查点续聊 | 第二期 | `run/checkpoint`；第一期用存档还原 |
| 调用链视图 | 第二期 | agent 把 span 经 OTLP 发给网页端 |
| 本机 hooks、`-approve-locally` | 第二期 | 只来自连接器的命令行 |
| macOS 上的 bash | 第三期 | Seatbelt（sandbox-exec），同 Codex、Claude Code |
| 浏览器、DeepSeek 搜索、长期记忆、知识库检索、Skills、MCP | 暂不支持 | 依赖服务器上的资源或会在本机执行服务器下发的命令，需要另行设计 |
| 评测、队列、指标 | 不适用 | 只在服务器上跑 |

## 10. 代码改动

| 位置 | 改动 |
| --- | --- |
| `main.go` | 新子命令 `tern connect` |
| `internal/connect`（新） | 连接器：参数与本机上限、WebSocket 客户端与重连、对话管理（启动 agent、Dial/Attach、转发、停止、退出上报）、参数过滤 |
| `internal/web` | `/connect` 端点与连接器登记；`.data/connectors.json`；设置页“本机连接”（新建、吊销、在线状态）；工作区下拉合入在线连接器的目录；`launchRemote`；远程对话的强制停止；租户功能 `connect` |
| `internal/web` 代理 | 远程对话按供应商名路由、服务器加密钥 |
| `internal/protocol` | 把一路 `run/data` 包成 `io.Reader`/`io.Writer`，给 `protocol.NewConn` 用；网页端和连接器两侧共用 |
| `internal/llm` | 代理模式按 `AGENT_PROXY_TOKEN` 判断；代理模式下不要求本机密钥；发 `X-Agent-Provider` |
| `internal/sandbox/seccomp.go` | 加 `//go:build linux`，其他系统上编译通过、只是不提供 bash（第一期顺带做，连接器才能在 macOS 上跑文件工具）。**已完成**（2026-10-23，另加 `seccomp_other.go`） |

WebSocket 用 `golang.org/x/net/websocket`：`golang.org/x/net` 已经在依赖里，不增加新模块；它不再加新功能，但收发文本帧、配合应用层心跳够用。

## 11. 分期与验证

- **第一期**：配对与吊销、连接与重连、远程目录、发起与续聊、审批与提问、停止、模型代理加密钥、Linux 上的文件工具与 bash、子 agent。
- **第二期**：撤销、沙箱文件面板、附图、检查点续聊、调用链、本机 hooks、`-approve-locally`。
- **第三期**：macOS 的 bash。

验证用真实模型、真实的两台环境：服务器跑网页端，另一台机器（或同一台机器上换一个用户和数据目录）跑连接器，在临时 clone 里做编码任务。要验证的情况：

1. 正常编码任务：读、改、跑测试，改动出现在连接器那边的目录里，服务器上没有这份代码的拷贝（存档里的模型请求除外）。
2. 断网重连：对话进行中断开连接器的网络 30 秒，恢复后页面补齐中间的过程，对话正常结束。
3. 越权：服务器传 `-permission-mode full`、`-hooks`、`-mcp-server`、不存在的目录名、`../` 路径，连接器都拒绝或降级，并在本机终端说明。
4. 吊销：对话进行中吊销令牌，连接立即断开，agent 收尾写好检查点。
5. 密钥：连接器所在机器上没有任何模型密钥，对话照常；改写 `X-Agent-Upstream` 指向别的地址，服务器不转发。

## 12. 需要确认的问题

1. 远程对话的模型请求要不要完整记进服务器存档？记的话页面、续聊、归因照常；不记的话代码片段不落服务器，但这些功能要改成从连接器取。设计按“记”写。
2. 连接器的权限上限默认 edit，还是更保守的 ask？
3. 租户账号能不能用：设计里需要租户开通 `connect`。
4. `-approve-locally` 放第二期是否可以，还是第一期就要。
