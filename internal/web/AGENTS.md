# web（网页端）的约束

- 网页端是 app-server 的客户端，不是服务端：这里只留登录与账号（`.data/users.json`，PBKDF2）、记住的主机（`.data/web-hosts.json`）、页面、`/mcp` 与 `/me`，其余每个 HTTP 接口都翻成对应的协议方法（spec 第 5 节）。对话、设置、文件、评测这些数据都在 app-server 里，这里不读 `.data/` 下的别的东西。
- 必须登录：账号在 `.data/users.json`；这一版只有平台账号，早期带 `tenant` 的账号登录时拒绝，提示“租户账号暂不支持”。需要知道是谁的请求时，协议参数里的 `user` 填登录账号名，app-server 用它取设置的个人层、记 agent 审计里的发起人，并把它记在审计记录的 Detail 里（界面名字 `tern web`）。
- 可以同时连几台主机，一台一条连接（主机表：别名 → `Client`、状态、`runs`；本机的别名是空串）。`-remote` 可以写多次：不带时先 `appclient.Start(false)` 再 `appclient.Dial("tern web")` 连本机；带了几台就只连这几台，每台 `appclient.DialRemote("tern web", 别名)` 经 ssh 连远端（远端由 proxy 自己 start）。命令行定的主机启动时连不上直接报错退出（远程时带上 ssh 的报错），不进重连。页面上连过的主机记在 `.data/web-hosts.json`（`{"hosts": ["devbox", ""]}`，0600，先写 `.tmp` 再 rename；所有账号共用），启动时在后台一台台连，连不上只记成 `failed`，不挡启动。启动时打印连上的各台的主机、`TERN_HOME` 与版本。要从源码跑 agent 用 `tern app-server -dev`。
- 远端的 proxy 命令优先用 `~/.tern/bin/tern`（网页里“装到远端”装的就是那里），没有才用登录 shell 的 PATH 上的 `tern`；两条路都走 `$SHELL -lc`，`~/.profile` 里的 `TERN_HOME` 才生效。连不上时 `appclient` 按 ssh 的退出码与 stderr 分类成 `RemoteError`（`auth`、`hostkey`、`unreachable`、`no-tern`、`protocol`、`other`），网页端照 `kind` 给按钮，不靠人读 stderr。
- 地址：本机的页面与接口不变（`/`、`/runs`、`/?run=…`）；远端主机的页面与数据接口带 `/h/<别名>` 前缀（`/h/devbox/runs`、`/h/devbox/?run=…`），剥掉前缀交给同一套路由，主机放进请求的 context；`/login`、`/logout`、`/me`、`/connection`、`/hosts`、`/mcp`、`/ui/`、`/mcp-oauth/callback` 不带前缀。没连上的主机回 404“没有连上主机 <别名>”，记住了但这次没连上的回 502（文字是那次的报错）。只带了 `-remote` 时本机没连，`GET /` 跳到第一台。
- MCP 的 OAuth 回调 `GET /mcp-oauth/callback` 不带主机前缀，网页端按 `state` 转给发起登录的那台：`POST /hub/mcp/{name}/login` 转给当前主机时，从返回的授权地址里取出 `state` 记下是哪台（只在内存里，10 分钟过期），回调拿 `state` 找回那台再转 `hub/mcp/login/finish`。这条路由不要求先登录：它来自授权页的跨站跳转，`SameSite=Strict` 的会话 cookie 不会带上，认领这次登录靠的就是 `state`；回调页登录成功后 `postMessage` 给 `window.opener`，页面据此刷新 MCP 中心。
- 参数与回复照 spec 第 5 节的写法：请求体里的 JSON 原样作为参数，路径与查询参数同名（对话的 `{id}` 叫 `threadId`），回复原样作为响应体。协议错误的 `code` 在 400–599 之间的原样作为 HTTP 状态和文字；重连期间回 503（文字是“正在重连 <host>”，错误带 `appclient.ErrReconnecting`）；其余（连接断了、协议本身的错误）回 502。
- 断线重连在 `internal/appclient`：本机模式和远程模式一样按 1、2、4…秒退避（最多 30 秒），本机每次先 start、远程重新起 ssh；重连期间那台的请求立刻失败，正在推送的事件流不结束（重连后 appclient 按每条订阅最后收到的 seq 重新 `thread/resume` 补齐）。网页端只负责把状态转给页面：`GET /connection` 是 SSE，推所有主机 `{hosts: [{alias, host, remote, state, runs, error, kind, hint, detail, providers, home, stale}]}`（`state` 是 `connected`、`reconnecting`、`failed`；`host` 本机用 `initialize` 报的主机名、远端用别名；连不上的带 `kind`、`hint` 与 ssh 的 `detail`；连上的远端带 `providers`、`home`，`stale` 表示那边的 build 和本机不同），连上先推一次、之后任何一台的状态或 `runs` 变了再推；`runs` 收到 `thread/started`、`thread/status/changed` 就加一，页面据此刷新对话列表。
- `/hosts`：`GET` 回还能连的候选（`appclient.SSHHosts()` 里还没连上的别名，本机没连时再加空别名），`POST {alias}` 连一台（空别名先 `Start(false)` 再 `Dial`，远端 `DialRemote`；连上就写进 `.data/web-hosts.json`），`DELETE /hosts/{alias}` 断开并从文件里删掉（本机用 `-`；只剩一台连上的主机时回 409“只剩这一台，不能断开”）。连不上回 502，响应体是 JSON `{error, kind, hint, detail}`：`kind` 是 `appclient.RemoteError` 的分类（`auth`、`hostkey`、`unreachable`、`no-tern`、`protocol`、`other`），`hint` 是给人看的下一步，`detail` 是 ssh 的原文，`error` 是两段拼起来的一整段话（没升级的页面照旧显示它）。连不上的那台也记住，菜单里才有那一项可以“确认指纹”“装到远端”。
- 远端的几条（只对远端，本机在路径里是 `-`）：`POST /hosts/{alias}/install` 把本机正在跑的程序拷到远端（`appclient.InstallRemote`，只拷同系统同架构；已经连着的那台先收掉再拨一次，远端的 proxy 才会换 app-server），`GET`/`POST /hosts/{alias}/hostkey` 取主机公钥与指纹、人看过以后写进 `~/.ssh/known_hosts`（`appclient.HostKeys`、`TrustHostKeys`），`GET /hosts/{alias}/keys` 列出本机有值的密钥名，`POST /hosts/{alias}/keys {names}` 把它们经 ssh 写进远端的 `.env`（`appclient.WriteRemoteKeys`）。
- 网页端只为“从本机复制密钥”读本机 `TERN_HOME/.env`，而且只在用户点了以后读，只读选中的那几个（同名环境变量优先，和 `llm` 读配置的规则一致），值经 ssh 的标准输入传给远端；其余数据照旧都在 app-server 里。
- 断开一台要关掉 `Client`：`appclient` 的 `Close` 会把还开着的订阅一起结束，页面那条事件流随之写完 `event: end`；当前主机断开后页面整页跳到剩下的第一台。
- 二进制内容（附图、沙箱文件、浏览器画面、导入的会话文件）按块：上传每块不超过 `appserver.MaxChunk`（4MB），最后一块带 `done` 与文件名；下载按 `size` 接着取。原来放在响应头里的信息照旧设：`Content-Type`、`X-Content-Type-Options: nosniff`、`Cache-Control`、`Content-Disposition`。
- 事件流 `GET /runs/{id}/events` 用那台主机的 `appclient` 的 `Subscribe`：每个事件写一条 `data:`，结束时写 `event: end`（对话结束、主机被断开都算结束）；浏览器断开就退订。
- 页面在 `page/`：浏览器原生 ES 模块，不打包，不引入构建工具与前端框架，按功能分文件；模块顶层只定义与挂事件，启动时的调用放 `main.js`。主页面的第三方脚本固定版本并带 SRI；模型写的可视化只在沙箱 iframe（`allow-scripts`，不给 `allow-same-origin`）里运行。`page/` 会整个打进程序，不要在里面放文档。
- 页面按地址上的 `/h/<别名>/` 认出当前主机（`util.js` 的 `HOST`），对话与数据的请求都走 `api(path)`，由它补前缀；`/login`、`/logout`、`/me`、`/connection`、`/hosts` 直接用原地址。输入框下方的 `#host`（观测页在顶栏）点开是主机菜单（连、断开、重试），左侧对话列表在连了几台时按主机分组，换主机一律整页跳转（`hostURL`），不动页面里按单台主机攒起来的状态。
- 导入对话只接受 Codex 的 rollout 与 Claude Code 的会话 JSONL，只读、不续聊（解析在 app-server）。

- 项目导航：`GET /workspaces` 代理 `workspace/list`；`PUT /runs/{id}/pin` 代理 `thread/pin`，user 仍由认证账号注入。项目树/筛选按主机与 workspace_id 分组；没有目录证据的老记录单列历史项目，不按显示名合并。

- 网页发送恢复：`GET /requests/{id}` 代理 request/status；事件流接受 Last-Event-ID/after，输出 snapshot（历史边界）、SSE id 与 data。X-Tern-Account 仅用于发现页面账号与已认证账号不一致，不作为身份凭证；不一致则返回 409 与 X-Tern-Account-Changed。草稿和不确定发送由页面按账号/主机/任务隔离保存，不直接读任务数据文件。

- 活动中心复用任务列表和服务端 waiting_requests；阅读游标与手动未读按账号/主机/任务保存在浏览器，不作为执行事实。批量已读固定点击时版本。分类系统通知经 Web Locks + 本地事件认领去重；不支持锁或存储失败时不发送，不保证关闭网页后通知。

- 子任务审批/提问按 task_id + original_call_id 对应工具轨迹，实际回答仍提交服务端 call_id；提问同时携带事件 request_id，防止旧控件答到新尝试。input_closed 只撤下对应 RPC 等待，父子共享状态引用保持一致。

- 队列编辑/排序使用服务端 version；发队首调用 queue/send，不再 messages 后 DELETE。未知发送结果保留独立回执查询；queue/add 复用草稿与请求回执。编辑冲突保留输入，用户核对服务器内容后明确切到最新版本；编辑草稿按账号、主机、任务、队列项与标签页隔离。

- 当前轮补充声明 managed=true，使用相同 request_id/client_id 和目标 turn_seq。待采纳状态来自 thread/list 的 steer_pending/steer_error；网页不自动补发。停止并发送提交确切 ID，状态未知时核对或显式转暂停队列；旧版浏览器残留只恢复草稿。/steer/refresh 与 /steer/resolve 仍只代理 app-server。

- 工作资源由 workpanel.js 统一管理标签，按账号/主机/任务保存描述与阅读偏好；数据仍来自任务事件和 app-server。关闭/恢复标签不执行或停止任务。异步文件读取只回填自己的资源状态，原始文件正文和认证数据不进偏好存储。
- GET /runs/{id}/workspace/file 代理 workspace/file/read；项目文件预览只显示受限读取的 UTF-8 文本，按内容版本和行号形成可编辑引用草稿，不直接发送。Markdown 本地文件链接只进入此路径，由服务端验证任务的实际目录，不在网页一侧读取宿主文件。
- 改动范围 scope/base 原样代理 workspace/diff；网页核对返回范围，不把旧版主机忽略参数的结果冒充新范围。行/文件反馈以账号/主机/任务隔离保存在浏览器，修改采用独立版本；加入输入前核对引用和意见版本，只追加草稿，保存或删除意见不调用模型。正文未完整预览、基准/文件变化或核对失败均保留原引用并明确状态。
- 子智能体、项目文件、diff 和浏览器过程统一放在右侧工作区标签中，桌面默认与聊天并排；窄屏覆盖展开。输入框上方的改动摘要属于最近读取的工作目录预览，普通目标条仅显示已保存目标，不冒充持续执行调度状态。
- 操作面板 tasks 用 thread/search 查完整历史；history 用 thread/find 的 after/through 分页，旧异步结果不得回填新账号/任务。/version 报实际运行 Web 与最近握手 backend 构建，不宣称源码已部署。Skill 引用和下一次压缩选项属于草稿，只有正常发送携带；只支持文字的插话/队列遇到这些引用应保留草稿并提示。
- 产物预览共享 artifact-preview.js。HTML 静态预览使用空 sandbox 和限制性 CSP，并移除主动导航元素；PDF.js 本地固定版本，关闭 eval/文档脚本，只绘制画布。项目2MiB、临时产物10MiB有界读取。批注按资源和版本保存，重新读版本后才加入草稿，不自动发送。
- 计划、持续Goal、项目操作和Git交付继续使用右侧workpanel资源标签。每个有副作用的按钮保存独立request_id回执；错误或断网不能当成未执行自动重发。表单/批注不能抢占主输入或自动发送。
- artifact-preview统一项目/临时产物的静态HTML/Markdown、图片和PDF；HTML无脚本/同源权限，PDF只用本地固定依赖绘制canvas。批注携带内容hash/来源/定位，加入草稿前核对版本；切换任务清理viewer生命周期。网页交互预览必须通过19的隔离运行门槛，不得用截图回放冒充。
