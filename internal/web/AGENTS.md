# web（网页端）的约束

- 网页端是 app-server 的客户端，不是服务端：这里只留登录与账号（`.data/users.json`，PBKDF2）、页面、`/mcp` 与 `/me`，其余每个 HTTP 接口都翻成对应的协议方法（spec 第 5 节）。对话、设置、文件、评测这些数据都在 app-server 里，这里不读 `.data/` 下的别的东西。
- 必须登录：账号在 `.data/users.json`；这一版只有平台账号，早期带 `tenant` 的账号登录时拒绝，提示“租户账号暂不支持”。需要知道是谁的请求时，协议参数里的 `user` 填登录账号名，app-server 用它取设置的个人层、记 agent 审计里的发起人，并把它记在审计记录的 Detail 里（界面名字 `tern web`）。
- 启动：`tern web [-addr …] [-remote 别名]`，没有 `-dev`。不带 `-remote` 时先 `appclient.Start(false)` 再 `appclient.Dial("tern web")` 连本机 app-server；带 `-remote` 时 `appclient.DialRemote("tern web", 别名)` 经 ssh 连远端（远端由 proxy 自己 start）。启动时第一次连不上直接报错退出（远程时带上 ssh 的报错），不进重连。所有请求共用这一条连接，启动时打印连上的 app-server 的主机、`TERN_HOME` 与版本。要从源码跑 agent 用 `tern app-server -dev`。
- 参数与回复照 spec 第 5 节的写法：请求体里的 JSON 原样作为参数，路径与查询参数同名（对话的 `{id}` 叫 `threadId`），回复原样作为响应体。协议错误的 `code` 在 400–599 之间的原样作为 HTTP 状态和文字；重连期间回 503（文字是“正在重连 <host>”，错误带 `appclient.ErrReconnecting`）；其余（连接断了、协议本身的错误）回 502。
- 断线重连在 `internal/appclient`：本机模式和远程模式一样按 1、2、4…秒退避（最多 30 秒），本机每次先 start、远程重新起 ssh；重连期间请求立刻失败，正在推送的事件流不结束（重连后 appclient 按每条订阅最后收到的 seq 重新 `thread/resume` 补齐）。网页端只负责把状态转给页面：`GET /connection` 是 SSE，推 `{host, remote, state, runs}`，连上先推一次、之后变了再推；`runs` 收到 `thread/started`、`thread/status/changed` 就加一，页面据此刷新对话列表。页面只多了顶栏的 `#host`（主机 · 连接状态，重连中换成醒目的样式）。
- 二进制内容（附图、沙箱文件、浏览器画面、导入的会话文件）按块：上传每块不超过 `appserver.MaxChunk`（4MB），最后一块带 `done` 与文件名；下载按 `size` 接着取。原来放在响应头里的信息照旧设：`Content-Type`、`X-Content-Type-Options: nosniff`、`Cache-Control`、`Content-Disposition`。
- 事件流 `GET /runs/{id}/events` 用 `appclient` 的 `Subscribe`：每个事件写一条 `data:`，结束时写 `event: end`；浏览器断开就退订。
- 页面在 `page/`：浏览器原生 ES 模块，不打包，不引入构建工具与前端框架，按功能分文件；模块顶层只定义与挂事件，启动时的调用放 `main.js`。主页面的第三方脚本固定版本并带 SRI；模型写的可视化只在沙箱 iframe（`allow-scripts`，不给 `allow-same-origin`）里运行。`page/` 会整个打进程序，不要在里面放文档。
- 导入对话只接受 Codex 的 rollout 与 Claude Code 的会话 JSONL，只读、不续聊（解析在 app-server）。
