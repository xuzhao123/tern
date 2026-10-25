# 远程主机：装到远端、连不上时说清原因、在网页里确认指纹、复制模型密钥

状态：已实现并完成收尾验证（2026-10-25），范围与证据见 [本批收尾记录](closeout-20261025.md)。

## 1. 为什么要改

网页端经 SSH 连远端（`internal/appclient/ssh.go`：`ssh -T -o BatchMode=yes <别名> 'exec "$SHELL" -lc "tern app-server proxy"'`），前提一大堆，全靠人自己准备：
- 远端装好 tern，并且在登录 shell 的 PATH 上；
- 两边协议版本一致；
- 远端 `TERN_HOME` 里有 `.env`（模型密钥）；
- 第一次连之前，先在终端里手动 `ssh` 一次确认主机指纹（`BatchMode=yes` 不会问）。

哪一步没做好，主机菜单里只显示 ssh 的 stderr 原文（`sshError`），看不出是哪一步、该怎么办。

Codex：`codex app-server daemon bootstrap` 在新机器上一条命令装好程序、起后台服务，`daemon update` 更新（`codex-rs/app-server-daemon/README.md`）；daemon 的命令都输出一行 JSON，客户端按字段判断。

## 2. 改什么

### 2.1 把本机的 tern 装到远端（2.1）

- `appclient` 加 `InstallRemote(alias)`：
  1. `ssh -T -o BatchMode=yes <别名> 'uname -s; uname -m'`，换成 Go 的写法（`Linux`→`linux`、`Darwin`→`darwin`；`x86_64`、`amd64`→`amd64`，`aarch64`、`arm64`→`arm64`），和本机的 `runtime.GOOS/GOARCH` 比。
  2. 不一样就报错：“远端是 linux/amd64，本机是 linux/arm64，程序没法直接拷过去。请在远端安装 tern（在源码目录里 `go install .`），并放进登录 shell 的 PATH。”
  3. 一样就把本机正在跑的程序（`/proc/self/exe`，没有时 `os.Executable()`）经 ssh 的标准输入传过去：`mkdir -p "$HOME/.tern/bin" && cat > "$HOME/.tern/bin/tern.new" && chmod 755 "$HOME/.tern/bin/tern.new" && mv "$HOME/.tern/bin/tern.new" "$HOME/.tern/bin/tern"`。
  4. 装完照常连一次。远端的 `proxy` 会按“程序不同就换”（app-server-auto-replace.md）把远端的 app-server 换成新的。
- **proxy 命令**改成优先用这个位置：远端有可执行的 `$HOME/.tern/bin/tern` 就用它，没有再用 PATH 上的 `tern`。还是走登录 shell（`~/.profile` 里的 `TERN_HOME` 照旧生效）。引号要处理好：这条命令先经本地 ssh 传给远端的 shell，再交给 `$SHELL -lc`。
- 网页端：主机菜单里，连不上的原因是 2.2 的“远端找不到 tern”或“协议版本不一致”时，那一项后面有按钮“装到远端”或“用本机的 tern 更新远端”。点了先 `confirm()`（“把本机的 tern 程序拷到 <别名> 的 ~/.tern/bin/tern？远端正在跑的对话不会被打断。”），再 `POST /hosts/{alias}/install`，成功后自动重连。已经连上的远端，如果 `initialize` 报的 build 和本机不同，菜单里那一项也有“用本机的 tern 更新远端”。

### 2.2 连不上时说清是哪一步（2.2）

- `DialRemote` 失败时按 ssh 的退出码和 stderr 分类，返回带种类的错误（`*RemoteError{Kind, Hint, Detail}`，`Detail` 是原来那几行 stderr）：

| `Kind` | 怎么认 | `Hint` |
| --- | --- | --- |
| `auth` | stderr 有 `Permission denied` | SSH 认证失败：确认 `~/.ssh/config` 里这台的 User、IdentityFile，能在终端里 `ssh <别名>` 不输密码登录（这里不会弹密码框） |
| `hostkey` | `Host key verification failed`、`No … host key is known` | 还没确认过这台机器的主机指纹 |
| `unreachable` | `Could not resolve hostname`、`Connection refused`、`timed out`、`No route to host`、`Network is unreachable` | 连不上 <别名>：检查网络，以及 `~/.ssh/config` 里的 HostName、Port |
| `no-tern` | 退出码 127，或 stderr 里有 `tern: command not found`、`tern: not found` | 远端找不到 tern |
| `protocol` | 握手时远端回协议版本不一致（`initialize` 的错误里两边的版本） | 两边的协议版本不一致 |
| `other` | 其余 | （只给 `Detail`） |

- 网页端 `POST /hosts` 连不上时，回 502，响应体改成 JSON `{error, kind, hint, detail}`（原来的纯文字放进 `error`，页面没升级时也能看）。启动时记住的主机在后台连不上，也照样记下 `kind`、`hint`。`GET /connection` 里那台的 `error` 照旧，另加 `kind`、`hint`。
- 主机菜单里连不上的那一项：先显示 `hint`，下面淡色的 `detail`；按 `kind` 给按钮：`hostkey` 是“确认指纹”（2.3），`no-tern`、`protocol` 是“装到远端”“用本机的 tern 更新远端”（2.1），其余是“再试一次”（原来就有）。

### 2.3 在网页里确认主机指纹（2.3）

- `appclient` 加 `HostKeys(alias)`：
  - 用 `ssh -G <别名>` 取 `hostname`、`port`；
  - `ssh-keyscan -T 5 -p <port> <hostname>` 取主机公钥；
  - 每行经 `ssh-keygen -lf -` 算出指纹（`SHA256:…` 和类型）。
- 再加 `TrustHostKeys(alias)`：把那几行 `ssh-keyscan` 的输出追加进本机的 `~/.ssh/known_hosts`。追加前再取一次，内容和给人看的那次不一样就报错不写，防止中途被换掉。
- 路由：`GET /hosts/{alias}/hostkey` 返回 `{host, port, keys: [{type, fingerprint}]}`；`POST /hosts/{alias}/hostkey` 写入。
- 页面：点“确认指纹”弹一个确认框，列出类型和指纹，并提示：“和你在那台机器上看到的一致再确认（在那台机器上：`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`）。”确认后写入，自动重连。

### 2.4 远端的模型密钥（2.4）

- `initialize` 的 `ServerInfo` 加 `Providers []string json:"providers,omitempty"`：这台机器上配好了密钥的供应商（对路由表里的每家调 `llm.LoadConfig`，成功的列进来）。
- 主机菜单里已连上的远端，`providers` 为空时显示“远端没有配模型密钥（<home>/.env）”和按钮“从本机复制密钥”。
- 点了以后列出本机 `.env` 里有值的那几个密钥的**名字**（`ARK_API_KEY`、`DEEPSEEK_API_KEY`…，取路由表里各家的 `KeyVars`），勾选，`confirm()`：“这些密钥会写进 <别名> 上的 <home>/.env（权限 0600），那台机器上能读你文件的人都能看到。继续？”
- `POST /hosts/{alias}/keys {names}`：网页端从本机 `TERN_HOME/.env` 读出这几个的值（同名环境变量优先，和 `llm` 读配置的规则一致），经 ssh 写进远端 `<home>/.env`：已有同名的那一行替换，没有的追加，文件不存在时从空配置创建，已有文件读失败则报错且不覆盖；写完 `chmod 600`；值经 ssh 的标准输入传，不出现在命令行上。写完提示“已写入，新对话生效”。
- **不做“密钥只放本机、远端请求绕回本机”**：那要让远端的 app-server 经界面的连接反过来请求本机，`internal/appserver/AGENTS.md` 定了不做 app-server 向界面发请求。写进 `codex-parity-plan.md` 2.4 的说明里。
- 读写密钥与 proxy 使用同一个登录 shell 的 `TERN_HOME`；未设置才用 `~/.tern`，避免自定义数据目录时写到另一份配置。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/appclient/ssh.go` | `proxyCommand` 优先用 `$HOME/.tern/bin/tern`；失败分类（`RemoteError`）；`InstallRemote`、`HostKeys`、`TrustHostKeys`、写远端密钥的函数 |
| `internal/appclient/client.go` 或 `rpc.go` | `DialRemote` 失败时返回 `RemoteError`；握手失败认出协议版本不一致 |
| `internal/appserver/handle.go` | `ServerInfo` 加 `Providers` |
| `internal/web/server.go` | `/hosts` 的失败带 `kind`、`hint`、`detail`；`/connection` 里带 `kind`、`hint`；新路由 `POST /hosts/{alias}/install`、`GET`/`POST /hosts/{alias}/hostkey`、`POST /hosts/{alias}/keys` |
| `internal/web/page/connection.js` | 主机菜单按 `kind` 显示说明和按钮；已连上的远端的“更新”“复制密钥” |
| `internal/web/page/app.css` | 相应样式 |
| `internal/web/AGENTS.md` | 补：网页端为“复制密钥”读本机 `TERN_HOME/.env`（只在用户点了以后读，只读选中的几个） |
| `internal/appclient/AGENTS.md`（有的话；没有就补在 `internal/web/AGENTS.md` 讲远程的那条） | 补：proxy 优先用 `~/.tern/bin/tern`；连不上时的分类 |
| `README.md` | “远程”一节：远端没装 tern 时可以从网页里装（同系统同架构）；连不上时会说明原因；可以在网页里确认指纹；可以把本机的密钥复制到远端 |

不改：`-remote` 的参数和别名的校验、重连与退避、协议版本 `Protocol`（`ServerInfo` 只加字段）。

## 4. 这一期不做

- 架构不同时自动在远端编译或下载。
- 密钥只放本机（见 2.4）。
- 远端 tern 的自动更新。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check internal/web/page/connection.js` 通过。
2. 在仓库里建临时目录放一个小程序（验完删掉），用几段典型的 ssh stderr 文字和退出码调分类函数，逐条报告得到的 `Kind`。
3. 读代码报告：新的 proxy 命令最终在远端执行成什么样（引号展开以后）；`uname` 结果的换算；写远端 `.env` 时值怎么传。

### Claude Code 在沙箱外做（真实模型，本机）

4. 本机模式不受影响：起网页端，新对话照常跑；`initialize` 的回复里 `providers` 列出本机配了密钥的几家。
5. 主机菜单里手填一个解析不了的别名（比如 `nosuch.invalid`）：显示“连不上 nosuch.invalid：检查网络…”，`kind` 是 `unreachable`。

### 用户自己测（需要一台能 SSH 上去的远端）

6. 远端没装 tern：主机菜单显示“远端找不到 tern”和“装到远端”，点了以后装好、连上，远端的 `~/.tern/bin/tern` 和本机程序一样。
7. 新机器第一次连：显示“还没确认过这台机器的主机指纹”和“确认指纹”，确认后连上，`~/.ssh/known_hosts` 多了那台的记录。
8. 远端没放 `.env`：连上后显示“远端没有配模型密钥”，“从本机复制密钥”后，远端的新对话能跑。
9. 本机重新 `go install` 以后：已连上的远端显示“用本机的 tern 更新远端”，更新后远端的 app-server 换成新的，正在跑的对话没断。
