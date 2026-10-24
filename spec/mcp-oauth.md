# MCP 的 OAuth 登录

状态：设计（2026-10-24），交给 tern 实现（codex-parity-plan.md 6.4）。本机没有要求 OAuth 的 MCP 服务，登录的实际流程由用户自己测。

## 1. 为什么要改

越来越多的远程 MCP server（GitHub、Linear、Notion 这类）要求 OAuth 登录。tern 连远程 MCP 只发普通的 HTTP 请求（`internal/mcp/mcp.go` 的 `Connect`），遇到 401 就连不上，这类 server 一个也用不了。Codex 有 `/mcp login <名字>`，用 rmcp-client 做 OAuth（`codex-rs/rmcp-client/src/oauth_http_client.rs`）。

tern 用的 mcp-go v1.1.1 自带 OAuth：`client.NewOAuthStreamableHttpClient`、`transport.OAuthHandler`（`GetAuthorizationURL`、`ProcessAuthorizationResponse`、`RegisterClient`），PKCE 的 `GenerateCodeVerifier`、`GenerateCodeChallenge`、`GenerateState`，还有 `IsAuthorizationRequiredError` 认 401。

## 2. 改什么

### 2.1 令牌存哪

- `$TERN_HOME/.data/mcp-tokens.json`，权限 0600，先写 `.tmp` 再 rename。按 MCP server 的 URL 存 `{"<url>": {"client_id", "client_secret", "token": {…mcp-go 的 Token…}}}`。
- 实现一个文件版的 `transport.TokenStore`，app-server 和 agent 共用（都在 `TERN_HOME` 下运行）。读写都拿 `.data/mcp-tokens.json.lock` 的文件锁。
- 令牌和密钥不经协议发给界面，页面只知道“已登录 / 没登录 / 要登录”。

### 2.2 连接时用令牌

- agent 连远程 MCP（`Connect` 里 URL 那条路）时：令牌文件里有这个 URL，就用 `NewOAuthStreamableHttpClient` 加文件版 `TokenStore`。过期的由 mcp-go 自己用 refresh token 换新的，并写回文件。
- 没有令牌、server 回了要求授权（`IsAuthorizationRequiredError`）：这个 server 跳过，事件里写“MCP 需要登录：<名字>，到 MCP 中心点登录”，和现在连不上的处理一样不拖垮整次运行。

### 2.3 登录流程（app-server + 网页端）

1. MCP 中心里，远程 server 的“测试连接”遇到要求授权时，这一项显示“需要登录”和“登录”按钮。
2. 点“登录”：网页端 `POST /hub/mcp/{name}/login` → app-server `hub/mcp/login/start {name, redirect}`。`redirect` 是网页端的回调地址 `<网页端的 origin>/mcp-oauth/callback`。app-server：
   - 建 `OAuthHandler`：`RedirectURI` 用这个地址，打开 `PKCEEnabled`；
   - server 支持动态注册且还没有 `client_id` 时，`RegisterClient(ctx, "tern")`，注册到的 `client_id`、`client_secret` 存进令牌文件；
   - 不支持动态注册时，回 400“这个 MCP server 不支持自动注册客户端：在 MCP 中心填上它给你的 client_id”。MCP 中心的这一项为此多两个可选输入框 `client_id`、`client_secret`，存进令牌文件，不存 `hub.json`；
   - 生成 `code_verifier`、`state`，记在内存里（`state` → server、verifier，10 分钟过期），返回 `GetAuthorizationURL` 给的地址。
3. 页面在新标签页打开这个地址，人在那边登录、授权。
4. 授权页跳回 `GET /mcp-oauth/callback?code=…&state=…`。这条路由不带主机前缀；网页端在第 2 步记下“这个 `state` 是哪台主机发起的”，把 `code`、`state` 转给那台的 app-server `hub/mcp/login/finish {state, code}`。app-server 用 `ProcessAuthorizationResponse` 换令牌、存进文件。
5. 回调页显示“已登录 <名字>，可以关掉这个页面”，并通知原页面刷新 MCP 中心（`postMessage` 给 `window.opener`，没有就算了）。
- MCP 中心里已登录的显示“已登录”和“退出登录”（`hub/mcp/logout {name}`：从令牌文件里删掉这个 URL）。
- 审计：登录成功、退出登录各写一条（`user_mcp_login`、`user_mcp_logout`，`Target` 是 server 名字），不写令牌。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/mcp/oauth.go`（新文件） | 文件版 `TokenStore`、令牌文件的读写与锁、建 `OAuthHandler` 的共用函数 |
| `internal/mcp/mcp.go` | `Connect` 对 URL 用令牌；要求授权时跳过并给出提示 |
| `internal/appserver/hub.go` | `hub/mcp/login/start`、`hub/mcp/login/finish`、`hub/mcp/logout`；`hub/list` 里每个远程 server 带 `auth: "none"|"required"|"logged_in"`；`client_id`、`client_secret` 的保存 |
| `internal/appserver/handle.go` | 方法表加三项 |
| `internal/web/server.go` | `POST /hub/mcp/{name}/login`、`POST /hub/mcp/{name}/logout`、`GET /mcp-oauth/callback`（不带主机前缀，按 `state` 找主机） |
| `internal/web/page/pages.js` | MCP 中心的“需要登录”“登录”“已登录”“退出登录”，`client_id`、`client_secret` 输入框 |
| `internal/web/AGENTS.md` | 补：`/mcp-oauth/callback` 不带主机前缀，网页端按 `state` 转给发起登录的那台 |
| `README.md` | MCP 那一行补：要求 OAuth 的远程 MCP 在 MCP 中心点“登录”，令牌存在 `.data/mcp-tokens.json` |

不改：stdio 的 MCP server、`hub.json` 的格式（`client_id` 不进 `hub.json`）、网页端内置的计算器 MCP。

## 4. 这一期不做

- 设备码登录（没有浏览器的机器）。
- 令牌按网页端账号分开（这一版一个 `TERN_HOME` 一份）。
- 命令行 agent 里交互式登录：命令行只用已经存下的令牌。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check internal/web/page/pages.js` 通过。
2. 读代码报告：令牌文件的锁与权限；`state` 的过期；回调按 `state` 找主机。

### Claude Code 在沙箱外做（本机）

3. 不受影响：内置计算器 MCP 照常在 MCP 中心测试连接成功，`auth` 是 `none`。
4. 加一个会回 401 的远程地址（本机起一个只回 `401` 和 `WWW-Authenticate` 头的小服务）：测试连接显示“需要登录”；agent 连它时跳过并给出提示，这一轮照常跑。

### 用户自己测（需要一个要求 OAuth 的远程 MCP）

5. 在 MCP 中心加这个 server，点“登录”，在新标签页里授权：回调页显示已登录；MCP 中心显示“已登录”；新对话里能用它的工具。
6. 过了令牌有效期（或者手动把令牌改成过期）后再用：自动刷新，`.data/mcp-tokens.json` 里的令牌变了。
7. “退出登录”后：再用它显示“需要登录”。
