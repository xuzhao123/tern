# web（网页端）的约束

- 网页端是 app-server 的客户端，不是服务端：这里只留登录与账号（`.data/users.json`，PBKDF2）、记住的主机（`.data/web-hosts.json`）、页面、`/mcp` 与 `/me`，其余每个 HTTP 接口都翻成对应的协议方法（spec 第 5 节）。对话、设置、文件、评测这些数据都在 app-server 里，这里不读 `.data/` 下的别的东西。
- 必须登录：账号在 `.data/users.json`；这一版只有平台账号，早期带 `tenant` 的账号登录时拒绝，提示“租户账号暂不支持”。需要知道是谁的请求时，协议参数里的 `user` 填登录账号名，app-server 用它取设置的个人层、记 agent 审计里的发起人，并把它记在审计记录的 Detail 里（界面名字 `tern web`）。
- 可以同时连几台主机，一台一条连接（主机表：别名 → `Client`、状态、`runs`；本机的别名是空串）。`-remote` 可以写多次：不带时先 `appclient.Start(false)` 再 `appclient.Dial("tern web")` 连本机；带了几台就只连这几台，每台 `appclient.DialRemote("tern web", 别名)` 经 ssh 连远端（远端由 proxy 自己 start）。命令行定的主机启动时连不上直接报错退出（远程时带上 ssh 的报错），不进重连。页面上连过的主机记在 `.data/web-hosts.json`（`{"hosts": ["devbox", ""]}`，0600，先写 `.tmp` 再 rename；所有账号共用），启动时在后台一台台连，连不上只记成 `failed`，不挡启动。启动时打印连上的各台的主机、`TERN_HOME` 与版本。要从源码跑 agent 用 `tern app-server -dev`。
- 地址：本机的页面与接口不变（`/`、`/runs`、`/?run=…`）；远端主机的页面与数据接口带 `/h/<别名>` 前缀（`/h/devbox/runs`、`/h/devbox/?run=…`），剥掉前缀交给同一套路由，主机放进请求的 context；`/login`、`/logout`、`/me`、`/connection`、`/hosts`、`/mcp`、`/ui/` 不带前缀。没连上的主机回 404“没有连上主机 <别名>”，记住了但这次没连上的回 502（文字是那次的报错）。只带了 `-remote` 时本机没连，`GET /` 跳到第一台。
- 参数与回复照 spec 第 5 节的写法：请求体里的 JSON 原样作为参数，路径与查询参数同名（对话的 `{id}` 叫 `threadId`），回复原样作为响应体。协议错误的 `code` 在 400–599 之间的原样作为 HTTP 状态和文字；重连期间回 503（文字是“正在重连 <host>”，错误带 `appclient.ErrReconnecting`）；其余（连接断了、协议本身的错误）回 502。
- 断线重连在 `internal/appclient`：本机模式和远程模式一样按 1、2、4…秒退避（最多 30 秒），本机每次先 start、远程重新起 ssh；重连期间那台的请求立刻失败，正在推送的事件流不结束（重连后 appclient 按每条订阅最后收到的 seq 重新 `thread/resume` 补齐）。网页端只负责把状态转给页面：`GET /connection` 是 SSE，推所有主机 `{hosts: [{alias, host, remote, state, runs, error}]}`（`state` 是 `connected`、`reconnecting`、`failed`；`host` 本机用 `initialize` 报的主机名、远端用别名），连上先推一次、之后任何一台的状态或 `runs` 变了再推；`runs` 收到 `thread/started`、`thread/status/changed` 就加一，页面据此刷新对话列表。
- `/hosts`：`GET` 回还能连的候选（`appclient.SSHHosts()` 里还没连上的别名，本机没连时再加空别名），`POST {alias}` 连一台（空别名先 `Start(false)` 再 `Dial`，远端 `DialRemote`；连上就写进 `.data/web-hosts.json`，连不上回 502 与 ssh 的报错），`DELETE /hosts/{alias}` 断开并从文件里删掉（本机用 `-`；只剩一台连上的主机时回 409“只剩这一台，不能断开”）。
- 断开一台要关掉 `Client`：`appclient` 的 `Close` 会把还开着的订阅一起结束，页面那条事件流随之写完 `event: end`；当前主机断开后页面整页跳到剩下的第一台。
- 二进制内容（附图、沙箱文件、浏览器画面、导入的会话文件）按块：上传每块不超过 `appserver.MaxChunk`（4MB），最后一块带 `done` 与文件名；下载按 `size` 接着取。原来放在响应头里的信息照旧设：`Content-Type`、`X-Content-Type-Options: nosniff`、`Cache-Control`、`Content-Disposition`。
- 事件流 `GET /runs/{id}/events` 用那台主机的 `appclient` 的 `Subscribe`：每个事件写一条 `data:`，结束时写 `event: end`（对话结束、主机被断开都算结束）；浏览器断开就退订。
- 页面在 `page/`：浏览器原生 ES 模块，不打包，不引入构建工具与前端框架，按功能分文件；模块顶层只定义与挂事件，启动时的调用放 `main.js`。主页面的第三方脚本固定版本并带 SRI；模型写的可视化只在沙箱 iframe（`allow-scripts`，不给 `allow-same-origin`）里运行。`page/` 会整个打进程序，不要在里面放文档。
- 页面按地址上的 `/h/<别名>/` 认出当前主机（`util.js` 的 `HOST`），对话与数据的请求都走 `api(path)`，由它补前缀；`/login`、`/logout`、`/me`、`/connection`、`/hosts` 直接用原地址。顶栏的 `#host` 点开是主机菜单（连、断开、重试），左侧对话列表在连了几台时按主机分组，换主机一律整页跳转（`hostURL`），不动页面里按单台主机攒起来的状态。
- 导入对话只接受 Codex 的 rollout 与 Claude Code 的会话 JSONL，只读、不续聊（解析在 app-server）。
