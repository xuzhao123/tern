# app-server 第二期：分叉、只退一边的撤销、一个网页端连几台主机

状态：已实现（2026-10-23），第 9 节是答复过的问题。两块都由 tern 实现，第 8 节的验收用真实模型通过；验收时发现的几处已补进第 3、4 节（标了“验收时发现”）。第 6–10 条里的“远端”是本机另外几个 `TERN_HOME`，经一个替身 ssh 连上（这台机器连不上 `ssh localhost`）；真 ssh 的通道在第一期验收过。接着 [app-server-design.md](app-server-design.md) 第 11 节的第二期；第一期已实现并提交（`4593d7c`）。

## 1. 目标与范围

- **分叉**：从一条对话的第 n 轮之后另起一条新对话，前 n 轮带过去，原对话不变，文件不动。协议加 `thread/fork`；chat 恢复 `/fork <n>`；网页端每轮加“分叉”。
- **只退一边的撤销**：`thread/rewind` 加 `mode`（`both`、`files`、`chat`）；chat 恢复 `/rewind <n> [both|files|chat]`；网页端的“撤销”改成能选这三种的对话框。第一期只留了 `both`（app-server-design.md 第 7 节）。
- **一个网页端连几台主机**：页面列出 `~/.ssh/config` 里的主机，点了就连，连过的记下来，下次启动自动连；左侧对话列表按主机分组。页面一次操作一台（当前主机）：新对话、设置、Skills/MCP、评测这些都是当前主机的。

不变的：agent、检查点格式、沙箱、权限；存档只多一种事件 `fork`。chat 仍然只连一台。

## 2. Codex 是怎么做的（依据）

本地的 Codex 仓库（`~/workspace/codex`，`970111f6cb`）：

- `thread/fork` 的参数是 `threadId` 和可选的 `lastTurnId`：保留到这一轮（含），之后的轮次不进分叉，这一轮不能还在进行（`codex-rs/app-server-protocol/src/protocol/v2/thread.rs:562`）。另有实验性的 `beforeTurnId`，以及模型、权限等覆盖项。
- 分叉把截断后的历史复制成一条新对话（`thread_processor.rs` 的 `truncate_rollout_after_turn_id`），新对话的 `forkedFromId` 记着来源（`thread_data.rs:219`），发 `thread/started`，并且自动订阅（`thread_processor.rs:5783`）。
- 远程连接文档（learn.chatgpt.com/docs/remote-connections）：桌面端读 `~/.ssh/config` 里具体的 Host 别名，只有通配符的忽略；在 Settings › Connections 里添加或启用主机。文档没写几台主机的对话怎么排。

照着做的：保留到哪一轮（含）、复制历史、在新对话上记来源、发 `thread/started`。不照的：不自动订阅，同 tern 的 `thread/start`，界面接着发 `thread/resume`；不做 `beforeTurnId` 和覆盖项。

## 3. 分叉与只退一边的撤销

### 协议

| 方法 | 参数 | 回复 | 网页端接口 |
| --- | --- | --- | --- |
| `thread/fork`（新） | threadId、turn（保留到这一轮，含；检查点ID，取法同 `thread/rewind` 的 turn） | 新对话的摘要；不自动订阅 | `POST /runs/{id}/fork`（新） |
| `thread/rewind` | 加 `mode`：`both`（默认）、`files`、`chat` | 同现在 | 同现在 |

- 摘要（`run.summary`）加 `fork_of`：分出来的对话是原对话的ID，其余为空。
- `appserver.Protocol` 改成 2。新网页端连上旧远端时，`initialize` 就报出两边的版本，而不是等点了分叉才报“没有这个方法”。

### app-server：`thread/fork`（`internal/appserver/server.go`，挨着 `threadRewind`）

先检查，按下面的顺序，不满足就回错误（code 的写法同第一期）：
- 对话不存在，404。
- 导入的对话（`Source` 不空）、评测的试次（`Eval` 不空），409“导入的对话不能分叉”或“评测的试次不能分叉”。这一条要排在核对轮次之前：导入的对话没有 `turn/started`，先核对轮次的话，总是回下一条的 404（第 1 块验收时发现，2026-10-23）。
- `turn` 不是这条对话的一轮（不在 `turnIDs` 里），404“这一轮不属于这条对话”。
- 对话正在跑，409“对话正在运行，停下之后再分叉”。原因：下面第 2 步要预览后面各轮改过的文件，预览要拿各轮检查点的锁。
- 这一轮已经撤销（`agent.Rewound`），409“这一轮已经撤销，不能从它分叉”。

再按下面四步做：
1. **复制事件**：从头复制到第 n 轮结束。第 n 轮的终点是它的 `turn/started` 之后下一个 `start` 或 `continue` 事件之前；n 是最后一轮就复制全部。只复制存档里有的事件：内存里的 `delta` 不带（`run.add` 本来就不把它写进存档）。事件原样复制，`seq` 不改，写进新对话的 `.data/runs/<新ID>.jsonl`（新的 UUID）。
2. **列出后面各轮改过的文件**：在原对话的 `turnIDs` 里，取第 n 轮之后第一个没撤销的轮次。有的话，跑 `rewind -dry-run -mode chat <那一轮>`（起 agent 的写法同 `threadRewind`），把回复里的 restore、delete、unsnapshotted 三张清单合起来。跑失败不挡分叉，只记下错误，比如后面已经分过叉、agent 报“之后有 2 个分支”。
3. **追加一条 `fork` 事件**：`text` 是原对话ID，`body` 是 `{from, turn, n, after, files, files_error}`：
   - `n` 是第几轮；
   - `after` 是原对话在这一轮之后还有几轮没撤销的；
   - `files` 是第 2 步合起来的清单；
   - `files_error` 是第 2 步的报错。
4. **建新对话**：新对话的 run `Done` 为真，`calls`、`seq`、`Query` 按复制的事件算（同 `loadRuns`），文件关上。`addRun` 发 `thread/started`，回复新对话的摘要。

### app-server：分支怎么续聊

- `canContinue` 碰到 `fork` 事件跳过，同 `allow`、`deny`、`rewind`。
- `lastCheckpoint` 碰到 `fork` 事件接着往前找，找到的是第 n 轮的 `turn/started`。最后一个事件不是 `exit 0`，`resumeHistory` 按现在的写法就会走 `checkpointHistory`，所以分支的第一轮从第 n 轮的检查点接着，`continues` 是第 n 轮，同撤销之后的续聊。
- `turn/start` 先查 `agent.Rewound(continues)`：已经撤销就回 409“接着的那一轮已经撤销，不能接着聊”，不起 agent。会走到这里的情况：分支还没跑过，原对话就撤销到了分叉点之前。不查的话，agent 起来以后才报错退出（`main.go:832`）。
- 沙箱目录与工作区：分支和原对话共用（第 9 节第 3 条）。沙箱目录按续接链的第一轮存放（`agent.ConversationRoot`），分支沿用原对话的第一轮。

### app-server：`thread/rewind`

- 加 `mode`，原样交给 agent 的 `rewind -mode`（`internal/agent/rewind.go:177`）。不是三个值之一就回 400。
- 分出来的对话里，只能撤销分叉之后它自己跑的轮次，也就是 `fork` 事件之后的 `turn/started`。撤销带过来的轮次，回 409“这一轮是分叉时从原对话带过来的，到原对话里撤销”。原因：agent 的撤销沿磁盘上的续接链往后找（`successors`）。分支还没跑过时，这条链会走进原对话后面的轮次，结果是还原原对话改过的文件，并把原对话的轮次标成已撤销。
- 原对话里，分支跑过一轮以后，再撤销分叉点之前（含）的轮次：agent 会拒绝，报“之后有 2 个分支，不知道磁盘上是哪一支的结果”（`rewind.go:96`）。这里不改（第 9 节第 4 条）。

### app-server：给模型的提醒

- agent 的 `turn/start` 本来就收 `notes`，作为提醒追加在问题后面（`main.go:425`），app-server 现在没传。
- 改成由 `turn/start` 生成：看上一个 `start` 或 `continue` 之后的 `fork`、`rewind` 事件，不另存。
- 文字沿用 `9abe0d0` 的 `internal/chat/chat.go:297`、`:314`。第几轮按 `turnIDs` 里的位置算；文件清单用“、”连起来。

| 事件 | 提醒 |
| --- | --- |
| `fork`，`files` 不空 | 对话接在第 n 轮之后另起了一支，但之后各轮对这些文件的改动仍在磁盘上，没有撤销：<files>。改之前先读当前内容。 |
| `fork`，`files` 空、`after` 大于 0、`files_error` 不空 | 对话接在第 n 轮之后另起了一支，原对话之后又跑了 <after> 轮，磁盘上的文件可能已经被改过。改之前先读当前内容。 |
| `rewind`，mode 为 `chat`，三张清单不空 | 对话退回到了第 n 轮之前，但之后各轮对这些文件的改动仍在磁盘上，没有撤销：<restore、delete、unsnapshotted>。改之前先读当前内容。 |
| `rewind`，mode 为 `files`，restore 或 delete 不空 | 用户撤销了第 n 轮及之后对这些文件的改动，磁盘上已恢复到那一轮之前：<restore、delete>。对话没有退回：上面记录里对这些文件的修改已经不在磁盘上，改之前先重新读。 |

### 网页端

- 新路由 `POST /runs/{id}/fork`，转成 `thread/fork`，请求体是 `{turn}`。
- `chat.js` 每轮问题下的操作（`chat.js:324` 的“撤销”旁边）加“分叉”：
  - 显示条件：这一轮有检查点、没撤销，对话已经结束（`r.exit`），不是导入的、评测的，也不在回放中。不要求有工作区。
  - 提示文字：“从这一轮之后另起一段对话：前面各轮带过去，原对话不变，文件不动”。
  - 点了不再确认。成功后打开新对话（`show`），并刷新列表。
- `runs.js` 处理 `fork` 事件：
  - 这时已有的各轮标成带过来的（`t.inherited`），它们不显示“撤销”。
  - 在第 n 轮后面加一条横幅：“从原对话第 n 轮之后分出来”，后接原对话的链接（`?run=<from>`）。`files` 不空时，再接一句“原对话之后各轮改过的这些文件仍在磁盘上：…”。
- 对话列表：`fork_of` 不空的，问题前加“分叉”标记，写法同 `SOURCE_LABEL`。
- `runs.js` 按 `rewind` 事件的 `body.mode` 显示。chat 做的只退一边，网页端也要显示对：
  - `both`：照旧。
  - `chat`：各轮照旧淡化，横幅写“已退回对话到第 a–b 轮之前，文件没动”。
  - `files`：各轮不淡化，横幅写“已撤销第 a–b 轮的文件改动：恢复 x 个文件，删除 y 个新建文件；对话没退”。
  - 三种都照旧把 `r.checkpoint` 设成 `continues`：`files` 时它就是最新一轮（`rewind.go:122`）。
- “撤销”改成对话框（第 9 节第 1 条）：
  - 现在是 `shell.js` 的 `rewindTurn`：先 dry_run 预览，再用 `confirm()` 确认。改成浏览器原生的 `<dialog>`，放在 `index.html`，样式在 `app.css`；不引入组件库。
  - 对话框上面是三个单选：文件和对话一起退（默认）、只退文件、只退对话；下面是预览，最下面是“撤销”和“取消”。
  - 每换一个选项，就带上这个 `mode` 重新 dry_run 一次，预览随之更新：
    - 一起退：同现在 `confirm()` 里的文字。
    - 只退文件：“只撤销第 n 轮及之后的文件改动，文件放回这一轮开始之前；对话不退，下一轮会告诉模型这些文件已经恢复。”后面列出恢复、删除、bash 改过的三张清单。
    - 只退对话：“对话退回第 n 轮之前，文件不动；下一轮会告诉模型之后各轮改过的文件还在。”后面列出这几轮改过的文件。
  - 冲突只挡“一起退”和“只退文件”：选这两种时有冲突，就列出冲突、灰掉“撤销”；“只退对话”不看冲突。
  - “撤销”按钮显示的条件不变（`chat.js:325`：有工作区等）。执行后同现在，重新 `show`。

### chat（`internal/chat/chat.go`）

- `/fork <n>`：
  - 先发 `thread/fork {threadId, turn: 第 n 轮的检查点ID}`，再同 `/resume <新ID>` 接上新对话（`resume`、`follow`）。
  - 打印“已分叉：新对话 <ID>，接在第 n 轮之后；原对话 <ID> 不变”。
  - 报错（比如还在跑时的 409）原样打出来。
- `/rewind <n> [both|files|chat]`：
  - 默认 `both`。
  - 预览与确认的文字按三种 mode 分别写，照 `9abe0d0` 的 `chat.go` 里的 `rewind`。`chat` 只退对话，不看冲突。
  - 提醒由 app-server 生成，chat 不再自己记。
- 顺带修第一期的一处（第 1 块验收时发现，2026-10-23）：
  - 现象：`-continue`、`/resume`、`/fork` 接上一条对话时，补齐已有事件的那一会儿，标准输入里已经到的行排进 `s.next`（`input` 里 `!s.running` 那一支）。之后主循环把它们拼成一个问题发出去，以 `/` 开头的命令也拼在里面。比如 `printf '/fork 2\n/turns\n' | tern chat -continue <ID>`，会把“/fork 2 /turns”当成第 4 轮的问题发给 agent，agent 报“没有 /fork 这个命令”。
  - 改法：主循环发 `s.next` 时逐行按收到的顺序看。以 `/` 开头的行按命令执行（同直接输入的命令），连着的几行普通输入照旧拼成一个问题发出去。比如 `/fork 2`、`你好`、`/turns` 三行，就是先分叉，再发“你好”这一轮，最后列轮次。
  - 命令自己要读的下一行，比如 `/rewind` 的“确认？[y/N]”，也先从排着的行里取，排着的取完了再读标准输入。不然 `printf '/rewind 3 files\ny\n'` 这种输入，确认会读到输入结束、判成“没有撤销”，排着的 `y` 又被当成问题发出去。
  - `resume` 不清空排着的行：那是还没发出去的输入，接上哪条对话就在哪条里发。
  - 一轮运行中输入的行照旧是插话，不在这次改动里。
- help 里这两行改回 `9abe0d0` 的写法：
  - `/fork <n>`：从第 n 轮之后另起一支（后面各轮留在原处，文件不动）。
  - `/rewind <n> [both|files|chat]`：撤销第 n 轮及之后。

## 4. 一个网页端连几台主机

### 主机从哪来

- `tern web` 不带 `-remote` 时，启动时连本机，同第一期。`-remote` 可以写多次：启动时连这几台，不连本机，同第一期的远程模式。启动时任何一台连不上，就报错退出。
- 启动以后可以在页面上再连别的主机，候选有三种：
  - `~/.ssh/config` 里具体的 Host 别名；
  - 启动时没连本机的话，加上“本机”；
  - 手填的别名或 `user@host`，校验同 `ValidAlias`。
- 记住页面上连过的主机（第 9 节第 2 条）：
  - 记在 `.data/web-hosts.json`：`{"hosts": ["devbox", ""]}`，空串表示本机，权限 0600。写法同 `users.json`：先写 `.tmp`，再 rename（`auth.go:97`）。
  - 这是这个网页端的设置，所有账号共用，不分个人。
  - 只记页面上连的：`-remote` 指定的只管这一次，不写进去。
- 启动时：
  - 先连命令行定的那几台，同第一期，连不上就报错退出。
  - 再在后台一台台连记住的、还没连上的。连不上不退出：那台记成“连不上”，带上报错，菜单里点它再试一次。
  - 放在后台连，是为了不让一台关了机的主机拖住启动：ssh 要等到连接超时才会报错。
- 断开：菜单里每台已连上的主机都有“断开”。
  - 断开就关掉连接（`Client.Close`），并从记住的列表里删掉。命令行连的那几台也能断开。
  - `Client.Close` 现在不结束订阅（`rpc.go:176`，以前只在进程退出时调用）。改成把还开着的订阅都结束，界面取到 `end`，网页端那台的事件流随之写 `event: end`。
  - 只剩一台时不能断开。当前主机断开后，整页跳到剩下的第一台。
- 读 `~/.ssh/config` 用新函数 `appclient.SSHHosts()`，放在 `internal/appclient/ssh.go`（桌面端以后也要用）：
  - 逐行找 `Host` 关键字，不分大小写，`Host a b` 和 `Host=a` 两种写法都认。
  - 值里含 `*`、`?`、`!` 的跳过，再过一遍 `ValidAlias`。
  - 去重，按出现的顺序排。
  - 不展开 `Include`、不看 `Match`（第 7 节）。文件不存在就是空列表。

### 地址

- 本机的接口和页面地址不变（`/runs`、`/?run=…`），旧链接照样能开。
- 远端主机的地址前面加 `/h/<别名>`，如 `/h/devbox/runs`、`/h/devbox/?run=…`。
  - 能带前缀的：页面本身 `GET /h/<别名>/`，以及所有对话与数据的接口，即 `internal/web/server.go` 里除 `/login`、`/logout`、`/me`、`/connection`、`/hosts`、`/mcp`、`/ui/` 之外的路由。
  - 不带前缀的就是本机。
- 网页端按前缀找到那台主机的连接。没连上的主机回 404“没有连上主机 <别名>”；记住了但连不上的回 502，文字是那次的报错。
- 本机没连时（只带了 `-remote`），`GET /` 跳到第一台的 `/h/<别名>/`。
- 没登录时，打开页面 `/h/<别名>/` 和打开 `/` 一样跳到登录页（`auth.go` 的 `protect`），接口照旧回 401。存下来的远端链接点开不能只看到一行“请先登录”（第 2 块验收时发现，2026-10-23）。
- 503“正在重连 <host>”、502 的规则不变，按请求的那台主机算。

### 网页端（`internal/web`）

- `server` 里的一条连接换成一张主机表：别名（本机为空）对应 `Client`、连接状态、`runs` 计数。
  - 现在直接用 `s.client` 的 5 处，改成用请求对应的那台：`s.call`（`server.go:195`，`reply` 和 `upload` 都经过它）、`download`（`binary.go:74`）、事件流（`server.go:398`）、`memory/forget`（`server.go:506`）、`connection`（`server.go:324`）。
  - `/h/{host}/` 前缀剥掉后交给同一套路由，主机放进请求的 context。
- `GET /connection` 改成推所有主机：`{hosts: [{alias, host, remote, state, runs, error}]}`。
  - `host` 是显示名：本机用 `initialize` 回复里的 host，远端用别名。
  - `state` 在第一期的 `connected`、`reconnecting` 之外，加一个 `failed`：记住的主机启动时没连上，`error` 是报错。
  - 任何一台的状态或 `runs` 变了就推。
- 新接口：
  - `GET /hosts` 回 `{candidates: […]}`：`SSHHosts()` 里还没连上的别名；本机没连时再加一项空别名，表示本机。
  - `POST /hosts {alias}` 连这一台，成功就写进 `.data/web-hosts.json`。空别名是本机，先 `Start(false)` 再 `Dial`；远端用 `DialRemote`。已经连上的直接回成功；连不上回 502，文字是 ssh 的报错。
  - `DELETE /hosts/{alias}` 断开这一台，并从 `.data/web-hosts.json` 删掉。只剩一台时回 409“只剩这一台，不能断开”。本机的别名用 `-` 表示（路径里不能是空的）。
- `-remote` 改成可以写多次。
- 第 2 块验收时发现、要补的三处（2026-10-23）：
  - **同一台不会连两次**：后台连记住的主机时，这台既不算连上也不算连不上，会出现在候选里；这时在菜单里点它，就和后台同时拨号。所以收下一条新连接时，这台如果已经在主机表里，就关掉新的这条、用原来的。
  - **连不上的本机**：记住了本机、启动时又没连上的，`/connection` 里 `remote` 是 false，页面显示“本机”，不是一台空名字的远端。
  - **握手超时的报错**：`appclient` 等 `initialize` 超过 `handshakeTimeout`（10 秒，`client.go:33`）时，报错写成“连 <别名> 超时：10 秒内没有完成握手”，有 ssh 的 stderr 就接在后面，不直接给“context deadline exceeded”。连一台关了机的主机时，ssh 要等到 TCP 超时才退出，菜单里看到的就是这一句。

### 页面（`internal/web/page`）

- `util.js` 加两样：
  - `HOST`：从 `location.pathname` 的 `/h/<别名>/` 取，本机为空。
  - `api(path)`：当前主机是远端时加上前缀。
- 页面里的请求都改用 `api`，共约 35 处 `fetch`、`EventSource`，还有 `refURL`（`util.js:60`）和浏览器画面（`browser.js:28`）。`/login`、`/logout`、`/me`、`/connection`、`/hosts` 不加前缀。
- 顶栏的 `#host` 照旧显示“当前主机 · 状态”，点开是主机菜单：
  - 上面是已经连上的各台，带状态，点哪台就整页跳到那台的地址；每台后面有“断开”，只剩一台时没有。
  - 连不上的（`failed`）也列在上面，显示报错，点它就是再连一次（`POST /hosts`）。
  - 下面是 `/hosts` 的候选和一个输入框。点了发 `POST /hosts`，成功就跳过去，失败显示报错。
- 左侧对话列表（`shell.js` 的 `loadRuns`）只连一台时和现在一样。连了几台时按主机分组：
  - 当前主机排在最上面。每组一个标题，写“主机 · 状态”，重连中换成醒目的样式。组里是那台的 `/runs`。
  - 点别的主机的对话，整页跳到那台的地址：本机是 `/?run=…`，远端是 `/h/<别名>/?run=…`。
  - 某一台的列表取不到（在重连，或者连不上），那一组只显示标题和状态。
  - 观测页的“加入对比”只出现在当前主机这一组。
- `connection.js`：任何一台的 `runs` 变了，就重新加载列表。
- 换主机用整页跳转，是为了不动页面的状态：`ui.run` 和各页面的缓存都假定只有一台主机，跳过去就全部重来。

## 5. 按文件的改动

| 位置 | 改动 |
| --- | --- |
| `internal/appserver/server.go` | `thread/fork`；`thread/rewind` 的 `mode`，以及带过来的轮次不能撤销；`canContinue`、`lastCheckpoint` 跳过 `fork` 事件；`turn/start` 生成 `notes`，并查 `continues` 有没有撤销；摘要加 `fork_of` |
| `internal/appserver/handle.go` | 方法表里登记 `thread/fork`（挨着 `handle.go:95` 的 `thread/rewind`） |
| `internal/appserver/appserver.go` | `Protocol` 改成 2 |
| `internal/appserver/AGENTS.md` | 分叉复制事件、共用工作区与沙箱；分支里只能撤销它自己跑的轮次 |
| `internal/appclient/ssh.go` | `SSHHosts()` |
| `internal/appclient/rpc.go`、`client.go` | `Close` 结束还开着的订阅；握手超时的报错 |
| `internal/web/auth.go` | 没登录时 `/h/<别名>/` 页面跳登录页 |
| `internal/web/server.go`、`binary.go` | 主机表；`/h/{host}/` 前缀；`/connection` 推所有主机（含 `failed`）；`GET`、`POST`、`DELETE /hosts`；`.data/web-hosts.json` 的读写，启动时在后台连记住的主机；`POST /runs/{id}/fork`；`-remote` 可以写多次 |
| `internal/web/page` | `util.js` 的 `HOST`、`api`，所有请求改用它；`chat.js` 的“分叉”，带过来的轮次不显示“撤销”；`runs.js` 的 `fork` 事件，以及按 mode 显示 `rewind`；`shell.js` 的 `rewindTurn` 改成对话框、分组列表和“分叉”标记；`connection.js` 和顶栏的主机菜单（含断开、重试）；`index.html` 的 `<dialog>` 与主机菜单，`app.css` |
| `internal/web/AGENTS.md` | 网页端可以同时连几台，一台一条连接；`/h/<别名>/` 前缀；主机菜单与分组列表；除了 `users.json`，还读写 `.data/web-hosts.json`（记住的主机） |
| `internal/chat/chat.go` | `/fork <n>`；`/rewind <n> [both|files|chat]`；help；接对话补齐期间排着的命令按命令执行 |
| `README.md` | `-remote` 可以写多次；主机菜单、记住的主机（`.data/web-hosts.json`）与断开；`/h/<别名>/` 地址；chat 的 `/fork` 和 `/rewind` 的三种 mode，网页端撤销的三种选法；分叉的限制（共用工作区与沙箱，分叉点之前不能撤销） |
| `AGENTS.md` | 结构一节补一句：网页端可以同时连几台主机 |

不要改的：
- agent，包括 `agent.Rewind` 和检查点格式；`internal/llm`、`internal/protocol`。
- 存档里已有的事件：只新加一种 `fork`。
- chat 连几台主机。

## 6. 交给 tern 时分两块

按顺序做，每块做完都能编译运行，review、验收后再做下一块。

1. 分叉与只退一边的撤销（第 3 节）：app-server、网页端的路由和页面（含撤销的对话框）、chat；`Protocol` 改成 2。
2. 一个网页端连几台主机（第 4 节）：`SSHHosts`、`Close` 结束订阅；网页端的主机表与前缀、记住的主机与断开；页面的 `api`、主机菜单与分组列表。

## 7. 这一期不做

- 分叉时换模型、权限（Codex 的覆盖项），以及 `beforeTurnId`。分叉以后续聊本来就能每轮换权限；换模型要新建对话，同第一期。
- 分叉评测的试次和导入的对话。
- 对话树：只在分支上记来源、显示链接，原对话里不显示分出去了哪些。
- 分支用自己的工作区或沙箱副本（第 9 节第 3 条）。
- 按账号分别记住主机；`~/.ssh/config` 的 `Include`、`Match`，以及用 `ssh -G` 解析。
- 网页端给没有工作区的对话也提供撤销：“撤销”按钮显示的条件不变。
- 跨主机分叉或搬对话。
- chat 连几台主机：chat 一次只看一条对话，`-remote` 够用。
- 远端主机上的计算器 MCP（`/mcp`），同第一期；桌面端（第三期）。

## 8. 验收

用真实模型，在临时 clone 里做。
- devbox 是 `~/.ssh/config` 里指向 `localhost` 的别名：远端没设 `TERN_HOME`，所以用 `~/.tern`；本机的网页端在 clone 的源码根目录里跑。
- 远端的 tern 用 clone 编出的程序，放在登录 shell 的 PATH 上。

分叉与撤销（第 1 块）：
1. **网页端分叉**：在登记了 clone 的工作区里开一条对话，三轮各用 `edit_file` 改一个文件（a.txt、b.txt、c.txt）。在第 1 轮点“分叉”：
   - 列表里出现带“分叉”标记的新对话。打开只有第 1 轮和一条横幅，横幅列出 b.txt、c.txt，并链回原对话。原对话三轮都在，没有变化。
   - 新对话的存档（`.data/runs/<新ID>.jsonl`）前面的行和原对话第 1 轮的行相同，最后一行是 `fork` 事件。
   - 在分支里问“这段对话里你改过哪些文件？只列文件名”，回答只有 a.txt。这一轮的模型请求里，问题后面带着“对话接在第 1 轮之后另起了一支……b.txt、c.txt……”的提醒。
   - 分支这一轮的检查点里，`continues` 是原对话第 1 轮的检查点ID。
2. **chat 分叉**：`tern chat -continue <原对话ID>`，`/fork 2`：打印出新对话ID，网页端列表里实时出现，`/turns` 是两轮。用管道一次给全输入（`printf '/fork 2\n/turns\n' | tern chat -continue <原对话ID>`）结果也一样，两条命令都按命令执行，没有发给 agent；`printf '/rewind 3 files\ny\n' | tern chat -continue <ID>` 真正撤销，`y` 没有发给 agent。
3. **只退一边**：
   - `/rewind 3 files`：c.txt 恢复了。网页端里第 3 轮不淡化，横幅写“对话没退”。下一轮的请求里带着“用户撤销了第 3 轮及之后……”的提醒。
   - 另开一条同样三轮的对话，`/rewind 2 chat`：b.txt、c.txt 不动；第 2、3 轮淡化，横幅写“文件没动”。下一轮的请求里带着“对话退回到了第 2 轮之前……”的提醒。
   - 网页端点“撤销”：对话框里换着选三种，预览跟着变。在终端里手动改一下 c.txt 造出冲突：选“一起退”“只退文件”时列出冲突、“撤销”灰掉；选“只退对话”可以撤销。用对话框各做一次“只退文件”“只退对话”，结果同上面 chat 的两条。
4. **该拒绝的拒绝**：
   - 对话在跑时分叉：409“对话正在运行，停下之后再分叉”。
   - 在分支里撤销第 1 轮：没有“撤销”按钮；直接调接口，回 409“这一轮是分叉时从原对话带过来的……”。
   - 分支跑过一轮以后，在原对话里撤销第 1 轮：报“之后有 2 个分支……”，原对话和分支的文件都没变。
   - 导入的对话没有“分叉”按钮；直接调接口，回 409。
5. **版本**：网页端连第一期的 app-server（`Protocol` 为 1），`initialize` 报出 1 和 2。

几台主机（第 2 块）：

6. **候选**：`~/.ssh/config` 里写 `Host *`、`Host dev-*`、`Host devbox devbox2`、`Host !x`，主机菜单的候选只有 devbox、devbox2（以及没连时的“本机”）。
7. **两台同时用**：在 clone 里 `tern web`，再从菜单连 devbox：
   - 左侧分成两组。点 devbox 组里的对话，地址变成 `/h/devbox/?run=…`。
   - 在 devbox 上新建对话：存档出现在 `~/.tern/.data/runs/`，不在 clone 的 `.data/runs/`。
   - devbox 的设置页显示的是 `~/.tern` 的设置；改一项，clone 的设置不变。
   - 在 devbox 上附一张接近 20MB 的图片，在文件面板上传、下载接近 10MB 的文件，分块传输经过前缀也能用。
8. **一台断了不影响另一台**：杀掉连 devbox 的 ssh 进程：
   - devbox 组显示重连中，它的请求回 503。
   - 本机组照常新建对话、续聊。
   - 恢复后 devbox 那边补齐中间的过程。
9. **只连远端**：`tern web -remote devbox -remote devbox2`（devbox2 可以是指向同一台的另一个别名）：
   - 启动时连上两台。
   - 不起本机的 app-server：本机原来没在跑的，之后也没有 `.data/app-server/app-server.pid`。
   - `GET /` 跳到 `/h/devbox/`。
10. **记住与断开**：
    - 从菜单连 devbox 以后，`.data/web-hosts.json` 里有 devbox。重启 `tern web`（不带 `-remote`），devbox 自动连上。
    - `-remote devbox2` 启动的那次，devbox2 没有写进文件。
    - 再记一台连不上的主机（`~/.ssh/config` 里 `Host deadbox`，`HostName 10.255.255.1`）：重启时网页端照常起来，不等它；菜单里 deadbox 显示“连不上”和 ssh 的报错。
    - 断开 deadbox：它从文件里删掉。断开 devbox：正在看的 devbox 对话的事件流结束，页面跳到本机。只剩本机时，菜单里没有“断开”。
11. **不退化**：只连本机时，页面和第一期一样，旧的 `/?run=…` 链接能打开；把第一期验收的第 1 条走一遍。

每块做完都要过：gofmt、`go vet ./...`、`go build`，改过的页面脚本过 `node --check`。

## 9. 已答复的问题（2026-10-23）

1. 网页端也提供“只退文件”“只退对话”：“撤销”改成能选三种的对话框（第 3 节“网页端”）。
2. 记住页面上连过的主机：记在 `.data/web-hosts.json`，启动时在后台自动连；菜单里可以断开，断开就不再记（第 4 节）。`internal/web/AGENTS.md` 里“网页端只读 `users.json`”的约束跟着改。
3. 分支和原对话共用工作区和 bash 沙箱目录。
4. 分支跑过以后，原对话不能再撤销分叉点之前（含）的轮次（agent 报“之后有 2 个分支”）；分支还没跑过时原对话先撤销到了分叉点之前，分支就接不上了（409）。都照这样，不改 agent 的 `Rewind`。
