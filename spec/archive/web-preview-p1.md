# 网页预览 P1：在右侧工作区操作本机开发服务

状态：已实现并验收（2026-10-31，tern 用 DeepSeek 实现，Claude Code review 与沙箱外验收）。验收中查出并补进 spec 的问题（标"review 时补"）：CDP 名字猜错编不过；页面刷新后动作编号撞车（改成每个页面视图一个 `client`）；代理放行第一条后裸转发，keep-alive 上的后续请求以 absolute-form 到开发服务；滚轮缺 `deltaX`；复用会话时不导航；并发请求乱序丢点击（改成按顺序发）；重定向出范围时报"导航失败"。

沙箱外（VM、临时 `TERN_HOME`、无头 Chrome 驱动网页端，开发服务 8601、别的服务 8602）连跑 5 次，每次 18 项全过：画面出现；同源 `fetch`、WebSocket 通；点击输入框输入 `abc` 再经输入法提交"中文"，开发服务收到 `abc中文`；点按钮、滚动、刷新生效；页面里用 `127.0.0.1`、`localhost`、`[::1]`、`127.0.0.1.nip.io` 访问 8602（fetch、图片、WebSocket），8602 命中 0 次，状态行显示拦下的数量；`file:///etc/hostname`、`chrome://version`、`http://localhost:8602/`、`http://example.com/`、`http://127.0.0.1:8601/`（授权的是 `localhost`）都 403 不导航；302 到 8602 停在空白页、`left_origin`；过期 `seq`、旧 `viewport` 的鼠标都 409。渲染进程 `Seccomp: 2`，没有 `--no-sandbox`；会话关闭、app-server 退出后 profile 目录都删掉。只有 API、没有 Playwright 的 20 秒里抓 53 端口：没有 Chrome 发起的 DNS 查询（只有同时在跑的评测查模型服务）。macOS 能交叉编译，没在 Mac 上实测（第 5 节第 6 条未验证）。

这是 [web-product/19-web-preview.md](../web-product/19-web-preview.md)（EXT-06）第 11 节的 **P1**：用户授权一个本机开发服务的地址，右侧工作区里出现能真实点击、输入、滚动、刷新的页面。浏览器的隔离照 19 号文档末节的原型结论走"Chrome 自有沙箱 + 只放行授权目标的代理"。agent 接管（P2）、元素/区域反馈（P3）、沙箱里的服务（P4）都不在这一期，19 号文档的范围不因此缩小。

## 1. 为什么这样做

- 19 号文档原来的命名空间方案在 VM 上起不来（Chrome 的沙箱和 bwrap 冲突）。2026-10-31 的原型证明：Chrome 不套 bwrap、保留自己的沙箱，所有流量经 `--proxy-server` 指到一个只放行授权目标的代理，加 `--proxy-bypass-list=<-loopback>` 让本机地址也走代理，其他本地端口、外网、DNS 重绑定、WebRTC 都过不去。
- 代理管不到的是非网络的地址：**CDP 直接 `Page.navigate` 到 `file://` 能读到本地文件**。所以 Chrome 的 CDP 只在 tern 进程里用，对外只暴露一组受限动作；导航前由 tern 检查 scheme 和 origin。

## 2. 改什么

### 2.1 新包 `internal/preview`

一个 app-server 里一个 `Manager`，管所有预览会话。一个会话（`Session`）对应一个对话（`threadId`）里一个授权的 origin，一个独立的 Chrome 进程。

**授权目标（grant）**：P1 只接受本机开发服务。

- 地址必须是 `http://` 或 `https://`，主机是字面的 `localhost`、`127.0.0.1` 或 `[::1]`，端口必填（1–65535）。不接受 userinfo、别的主机名、别的 scheme。
- origin 规范化为 `scheme://host:port`（`localhost` 不改写成 IP）。授权只认这一个 origin；同 origin 的任意路径都可以。
- 授权只能由网页端的已认证用户创建（2.3 的 `preview/open`）。模型输出的链接不创建授权。

**代理**（`proxy.go`）：每个会话一个，监听 `127.0.0.1` 的随机端口，只给这个会话的 Chrome 用。

- 普通 HTTP 请求（absolute-form）和 `CONNECT`（https、wss）都处理。目标的 `host:port` 必须**字面等于** grant 的主机和端口（不解析域名、不认别名），否则回 403 并计数（`denied`）。
- 放行的连接一律连到固定地址：grant 主机是 `localhost` 或 `127.0.0.1` 时连 `127.0.0.1:<端口>`，是 `[::1]` 时连 `[::1]:<端口>`，不按请求里的名字解析。
- HTTP 请求原样转发（去掉 `Proxy-*` 头），不改响应头、不改 CORS/CSP/cookie。WebSocket 升级（`Connection: Upgrade`）在普通 HTTP 请求里出现时，同样连到固定地址后双向转发字节。
- 拒绝的请求只记 `host:port` 和次数，不记路径和查询参数。
- **普通 HTTP 请求逐条处理**（review 时补）：Chrome 会复用到代理的 keep-alive 连接，一条连接上先后有多条请求，可能是不同的 host。所以不能放行第一条之后就裸转发：每条请求都要单独判断放行、改成 origin-form、转给目标、读完响应（`http.ReadResponse`）再写回 Chrome，然后读下一条。只有 `CONNECT` 和 WebSocket 升级（响应是 101）之后才改成双向裸转发。沙箱外验收里，裸转发让后续请求以 `GET http://localhost:8601/api/...` 的 absolute-form 到了开发服务，Node 这类服务按路径路由就全错了。

**Chrome**（`preview.go`）：用 chromedp 的 `NewExecAllocator` 起，**不加** `--no-sandbox`（`CHROME_NO_SANDBOX=1` 时预览不可用，原因写"预览要求 Chrome 自己的沙箱"），`CHROME_PATH` 照 `internal/browser` 的规则。参数：

- 无头；`--user-data-dir` 是会话私有临时目录（0700，在 `os.TempDir()` 下，不在工作区、不在 `.data`），会话结束删除；
- `--proxy-server=http://127.0.0.1:<代理端口>`、`--proxy-bypass-list=<-loopback>`；
- `--force-webrtc-ip-handling-policy=disable_non_proxied_udp`、`--dns-prefetch-disable`；
- 关掉后台联网：`--disable-background-networking`、`--disable-component-update`、`--disable-sync`、`--no-first-run`、`--disable-default-apps`、`--metrics-recording-only`、`--disable-domain-reliability`（和 `internal/sandbox` 里 Chrome 包装的意图一致）；
- 下载：`Browser.setDownloadBehavior` 设 `deny`；
- 焦点：会话的页面开 `Emulation.setFocusEmulationEnabled(true)`（review 时补：无头 Chrome 里页面没有焦点，点输入框不会获得焦点、按键和 `insertText` 都被丢掉，沙箱外实测如此）；
- 新窗口、弹出页：监听新 target，非本会话的页面一律关掉，状态里记一次 `popup_blocked`。

**受限动作**：会话对外只有下面这些，没有任意 CDP、任意 JavaScript：

| 动作 | 参数 | 做什么 |
| --- | --- | --- |
| `navigate` | `url` | 先检查：scheme 是 http/https，origin 等于 grant；不等回 403，不导航 |
| `reload`、`back`、`forward` | | `back`/`forward` 之后检查当前 origin，不等于 grant 就再导航回 grant 的根路径 |
| `resize` | `width`、`height`（320–2560 × 240–1600） | `Emulation.setDeviceMetricsOverride`，视口版本 `viewport` 加 1 |
| `mouse` | `type`（move/down/up/wheel）、`x`、`y`、`button`、`clicks`、`dx`、`dy`、`modifiers` | `Input.dispatchMouseEvent`；坐标是视口 CSS 像素。wheel 时 Chrome 要求 `deltaX`、`deltaY` 都在，而 cdproto 的这两个字段带 `omitzero`，0 会被省掉（review 时补）：wheel 用本地的参数结构体（两个字段不带 omitempty）发 |
| `key` | `type`（down/up）、`key`、`code`、`text`、`modifiers` | `Input.dispatchKeyEvent` |
| `text` | `text`（最多 4096 字） | `Input.insertText`（中文输入法的提交走这里） |

- 每个动作带 `client`（页面每次建视图时随机生成的字符串，最长 64）、`seq`（这个 `client` 内递增）和 `viewport`。服务端按 `client` 分别记最后收下的 `seq`（最多记 32 个 `client`，超出时丢最久没动的）；同一个 `client` 的 `seq` 不大于上一个已收的回 409（重复或过期，不再执行）；新的 `client` 从任意 `seq` 开始。app-server 自己发起的动作（例如复用会话时的导航）不经过这套编号。`viewport` 和当前不一致的 `mouse` 回 409（尺寸变了，旧坐标不能用）。（review 时补：原来整个会话一个 `seq`，页面刷新、第二个标签页、页面重建视图时把 `seq` 归零，第一个动作必然 409；沙箱外验收里反复出现。会话描述里的 `seq` 字段去掉，页面也不再同步它。）
- 动作在一个会话内串行执行（一个 goroutine 一个有界队列，满了回 429）。
- `navigate` 时 Chrome 报加载失败（例如重定向到别处、被代理 403），先看主框架停在哪里：离开了授权 origin 就照下一条停到空白页并返回成功（状态里有 `left_origin`），不报"导航失败"（review 时补）。
- 页面主框架每次导航完成后检查 URL：origin 不等于 grant（例如服务端重定向到别处）就停在 `about:blank`，状态记 `left_origin`，不自动跟过去。

**画面**：`Page.startScreencast`（JPEG，质量 70，最大宽高等于视口），每帧收到后 ack，内存里只留**最新一帧**和它的序号 `frame`。不写盘、不进对话存档。

**生命周期**：

- 状态：`opening → ready`，失败 `failed`（带原因），关掉 `closed`。
- 一个对话同时最多一个会话；再 `open` 同一 origin 复用会话，并导航到这次给的地址（review 时补：停在空白页时用户在地址栏回车，要回到页面），换 origin 先关旧的。
- 30 分钟没有取画面、也没有动作就自动关。app-server 退出时全部关掉，删私有目录。对话删除时关掉它的会话。

### 2.2 app-server

新文件 `internal/appserver/preview.go`，方法（`handle.go` 的方法表加上）：

| 方法 | 输入 | 返回 |
| --- | --- | --- |
| `preview/open` | `threadId`、`url`、`width`、`height` | 会话描述（见下） |
| `preview/get` | `threadId` | 会话描述；没有会话回 404 |
| `preview/action` | `threadId`、`action`（2.1 的一种）及其参数、`client`、`seq`、`viewport` | `{ok: true}` 或错误（403/409/429） |
| `preview/frame` | `threadId`、`after`（上次拿到的帧序号）、`wait_ms`（最多 10000） | 有比 `after` 新的帧就立刻回 `{frame, width, height, data(base64 JPEG)}`；等到超时回 `{frame: after}`（没有新帧） |
| `preview/close` | `threadId` | 空 |

会话描述：`{state, origin, url, viewport, width, height, frame, denied, left_origin, popup_blocked, reason}`。`left_origin` 是布尔（主框架离开了授权 origin、停在空白页，下一次成功的 `navigate` 清掉），`popup_blocked` 是关掉的弹出页个数。不返回 CDP 地址、代理端口、profile 路径。

- 只接受界面连接的调用；agent 的连接（`run.handle`）不分派这些方法（P2 再说）。
- `open`、`close` 写审计：`user_preview_open`、`user_preview_close`，`Target` 是 origin，不写路径和查询参数。
- 评测的试次、导入的对话不能开预览（409）。
- `initialize` 的 `ServerInfo` 加 `Preview string json:"preview,omitempty"`：能用时为空；不能用时写原因（没有 Chrome、`CHROME_NO_SANDBOX=1`、不是 Linux 或 macOS）。是否能用在 app-server 启动时判断一次（找得到 Chrome 可执行文件即可，不真的起浏览器）。
- 单帧超过协议二进制块的上限（4 MiB）时丢掉这一帧，不改协议上限。

### 2.3 网页端

`internal/web/server.go` 的路由（远程照旧在 `/h/<alias>` 下）：

- `POST /runs/{id}/preview` `{url, width, height}` → `preview/open`
- `GET /runs/{id}/preview` → `preview/get`
- `DELETE /runs/{id}/preview` → `preview/close`
- `POST /runs/{id}/preview/action` → `preview/action`
- `GET /runs/{id}/preview/frame?after=N&wait=MS` → `preview/frame`，响应体直接是 JPEG（`Content-Type: image/jpeg`，帧序号放在响应头 `X-Preview-Frame`，宽高放 `X-Preview-Width`、`X-Preview-Height`）；没有新帧回 204。

新文件 `internal/web/page/preview.js`，工作区新标签类型 `preview`（`workpanel.js` 的 `kinds` 和"＋"菜单加"网页预览"，`agents.js` 的渲染表加 `preview: renderPreview`）：

- 顶部：地址栏（默认 `http://localhost:`，回车打开）、刷新、后退、前进、关闭；一行状态：`<执行主机> · <origin> · <状态>`，`denied > 0` 时写"拦下 N 个不在授权范围的请求"；`left_origin`、`popup_blocked`、`failed` 各有一句说明。
- `initialize` 报了 `preview` 原因时，标签里只显示原因，没有地址栏。
- 画面：一个 `<img>`，循环长轮询 `frame?after=`，拿到就换图。标签不可见或换了对话时停止轮询。
- 输入：画面元素可聚焦（`tabindex=0`）。聚焦时把 pointer、wheel、keydown/keyup 转成动作；坐标按 `<img>` 显示尺寸和视口尺寸换算成视口 CSS 像素。输入法：`compositionend` 发 `text`，组合过程中不发 `key`；普通可打印字符只发 `key`（带 `text`），不另发 `text`，避免输入两次。
- 动作按顺序发：页面一次只有一个 `preview/action` 请求在路上，下一个等上一个回来再发；排队期间连续的 `mouse move` 只留最后一个（review 时补：并发的请求会乱序到达，先到的大 `seq` 让后到的 `mousedown` 被当成过期丢掉，沙箱外验收里出现过）。
- 视口跟随画面区域大小：`ResizeObserver` 去抖 300ms 后发 `resize`，之后的鼠标动作带新 `viewport`。
- 回答里的链接不自动打开预览（链接行为不改）；P1 只从"＋ → 网页预览"进入。

### 2.4 不变的

`open_page`、`web_search`、`browser/frame` 和它的页面不改；`internal/browser` 不改；bash 沙箱不改；协议的行长和块大小上限不改。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/preview/preview.go`（新） | `Manager`、`Session`、Chrome 启动参数、受限动作、screencast、生命周期 |
| `internal/preview/proxy.go`（新） | 固定目标代理（HTTP、CONNECT、WebSocket 升级） |
| `internal/preview/AGENTS.md`（新） | 约束：不暴露任意 CDP/JS；导航前查 scheme 与 origin；不加 `--no-sandbox`；profile 私有、会话结束删除 |
| `internal/appserver/preview.go`（新） | 2.2 的五个方法、审计、对话删除时关会话 |
| `internal/appserver/handle.go` | 方法表；`ServerInfo.Preview` |
| `internal/appserver/appserver.go` | 建 `Manager`、退出时关闭 |
| `internal/appserver/threads.go` | `threadDelete` 里关掉这条对话的预览会话（review 时补） |
| `internal/web/server.go` | 2.3 的路由；`hostInfo` 带 `initialize` 的 `preview`，页面从 `/connection` 拿到不可用的原因（review 时补） |
| `internal/web/page/preview.js`（新） | 标签内容与输入 |
| `internal/web/page/workpanel.js`、`agents.js` | 新标签类型与渲染注册、"＋"菜单 |
| `internal/web/page/app.css` | 样式 |
| `README.md` | 网页端那段补一句："＋ → 网页预览"打开本机开发服务（`localhost`/`127.0.0.1`/`[::1]` 加端口），能点、能输入；浏览器保留自己的沙箱，网络只通到这个地址 |

不要改：`internal/browser`、`internal/sandbox`、`internal/protocol` 的上限、现有页面的链接行为。

## 4. 这一期不做

- agent 操作预览（P2）；元素/区域反馈、截图加入输入（P3）；预览沙箱里起的服务（P4）。
- 回答里的链接直接进预览。
- 非本机的目标、跨 origin 的第二个授权。
- 会话跨 app-server 重启保留。

## 5. 验收

### tern 自己做（沙箱里）

1. `gofmt -l .` 无输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过 `preview.js`、`workpanel.js`、`agents.js`。
2. 读代码报告：代理怎么判断放行、放行后连到哪里；`navigate`/`back`/`forward` 和服务端重定向分别怎么守住 origin；`seq`、`viewport` 的 409 规则；Chrome 的完整启动参数；会话什么时候关。

### Claude Code 在沙箱外做（VM，临时 `TERN_HOME`，无头 Chrome 驱动网页端）

1. 起一个本机开发服务（页面、同源 `fetch`、同源 WebSocket、一个输入框）和一个"别的服务"（记录每次命中）。网页端"＋ → 网页预览"打开开发服务：画面出现；点击、输入中文、滚动、刷新都生效（开发服务的日志对得上）。
2. 开发服务页面里用 `127.0.0.1`、`localhost`、`[::1]`、`127.0.0.1.nip.io` 访问"别的服务"（fetch、图片、WebSocket）：别的服务命中 0 次，状态行显示拦下的数量。
3. 地址栏输入 `file:///etc/hostname`、`http://localhost:<别的端口>`、`http://example.com`：都拒绝，不导航。开发服务 302 到别的端口：停在空白页，状态显示离开了授权范围。
4. 渲染进程 `Seccomp: 2`，命令行没有 `--no-sandbox`；会话关闭后 profile 目录不存在。
5. `tcpdump -i any port 53` 期间开一次预览：没有 Chrome 发起的 DNS 查询。
6. macOS：在 Mac 上跑一次第 1、2 条（需要用户的 Mac，做不了就写明未验证）。
