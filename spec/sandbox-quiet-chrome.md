# 沙箱里的 Chrome 不再一个个弹联网审批；模块缓存目录不存在时沙箱照常起

状态：已实现（2026-10-24，tern 实现；Claude Code 用真实模型验收）。验收时发现只靠参数关不干净，加了 PAC（第 2.1 节“验收时发现”）；review 时发现 PAC 参数没加引号，已修。

## 1. 为什么要改

**7.3**：tern 改页面时，会在沙箱里用无头 Chrome 加载页面检查。Chrome 自己有后台联网：组件更新、安全浏览、优化提示、自动填充等。沙箱的联网代理把每个域名都当成一次联网审批推给界面。2026-10-23 实现 app-server 第二期时碰到十来个；2026-10-24 实现插话时一下弹了 6 个，实现工作区选择框时又是 3 个加上 jsdelivr，串联因此停下来等人。这些请求没有一个是任务需要的，只能逐个拒绝。

现在 bash 工具的说明里给了启动 Chrome 的命令（`internal/sandbox/sandbox.go:199`），但没有关后台联网的参数。模型要是自己写测试脚本、自己拼参数，就更没有了。

**7.4**：沙箱把宿主机的 Go 模块缓存只读挂到 `/opt/gomod`（`sandbox.go:527` 附近，`--ro-bind g[1] /opt/gomod`）。这个目录不存在时（新机器上没下载过任何模块，或者 `HOME` 换了），bwrap 直接报 `Can't find source path …/go/pkg/mod`，整个沙箱起不来，连 `ls` 都跑不了。2026-10-24 验收内置命令时碰到过。

## 2. 改什么

### 2.1 沙箱里的 `chrome` 命令固定关掉后台联网

- 沙箱里放一个同名的包装脚本，放在 `PATH` 最前面的 `/opt/chrome-bin/` 下。名字就是现在说明里用的那个命令名（`front().Chrome`，比如 `google-chrome`）。脚本内容：

  ```sh
  #!/bin/sh
  # tern 的沙箱：Chrome 的后台联网（更新、安全浏览、优化提示等）不是任务需要的，固定关掉。
  exec <真正的 Chrome> --disable-background-networking --disable-component-update \
    --disable-sync --disable-default-apps --disable-domain-reliability \
    --disable-client-side-phishing-detection --no-first-run --no-default-browser-check \
    --metrics-recording-only --safebrowsing-disable-auto-update \
    --disable-features=OptimizationHints,OptimizationGuideModelDownloading,AutofillServerCommunication,Translate,MediaRouter \
    "$@"
  ```

  “真正的 Chrome”：Chrome 挂在 `/opt/chrome` 时（`front().ChromeDir` 不为空）是 `/opt/chrome/<名字>`；装在 `/usr` 下时是宿主机上的绝对路径（`front()` 里 `LookPath` 找到的那个，`/usr` 在沙箱里本来就看得到）。
- 怎么放进去：用 bwrap 的 `--perms 0755 --ro-bind-data <fd> /opt/chrome-bin/<名字>`（本机 bwrap 0.11.1 支持）。脚本内容写进一个 `os.Pipe`：几百字节，放得进管道缓冲，写完关掉写端，读端作为 `cmd.ExtraFiles` 的第二个文件，也就是 fd 4（第一个是 seccomp 程序，fd 3，`sandbox.go:471`）。`PATH` 在 `/opt/chrome:` 前面再加 `/opt/chrome-bin:`。
- **验收时发现（2026-10-24）**：只靠上面这组参数关不干净。本机 Chrome 155 带着这组参数加载一个本地页面，仍会去连 `accounts.google.com`、`update.googleapis.com`、`www.google.com`、`www.gstatic.com`（在宿主机上用一个只记录请求的代理试出来的；加 `NetworkTimeServiceQuerying`、`--disable-extensions`、`--disable-field-trial-config`、`--host-resolver-rules` 都没用）。所以包装脚本再加一个 PAC：
  - `--disable-features=` 那一项补上 `NetworkTimeServiceQuerying`（关掉 `clients2.google.com/time` 的请求）；
  - 加 `--proxy-pac-url=data:application/x-javascript-config;base64,<PAC 的 base64>`，PAC 是：
    ```js
    function FindProxyForURL(url, host) {
      if (/(^|\.)(google\.com|googleapis\.com|gstatic\.com|gvt1\.com)$/.test(host)) return "PROXY 127.0.0.1:9";
      return "PROXY 127.0.0.1:<沙箱代理的端口>";
    }
    ```
    Google 家的域名指向一个不通的端口，直接失败、不走沙箱的代理，也就不弹审批；别的域名照旧走沙箱的代理（端口用 `proxyPort`），照旧问人。用 base64 是为了不用处理引号。
  - 代价：沙箱里的 Chrome 连不上 Google 家的任何域名（包括 Google Fonts），页面里引用的这类资源会静默失败。bash 工具说明里那句补上：“Google 的域名在这个 Chrome 里一律连不上；真要用就用 curl（会照常申请联网）。”
  - 宿主机上实测：带上 PAC 以后这 4 个域名都不再出现，DOM 照常输出，页面里的 `https://example.com/x.png` 照旧走代理。
- 没有 Chrome 时（`front().Chrome` 为空）什么都不加。
- bash 工具说明里那句启动命令不变（命令名一样），后面补一句：“这个命令已经关掉了 Chrome 自己的后台联网；自己写脚本启动 Chrome 时也用它，别直接调 /opt/chrome 下的程序。”

### 2.2 兜底：明摆着是 Chrome 后台的域名直接拒绝，不问人

- `internal/sandbox/network.go` 加一张固定的表 `backgroundHosts`：
  - `update.googleapis.com`、`clients2.google.com`、`clients4.google.com`
  - `clientservices.googleapis.com`、`redirector.gvt1.com`、`edgedl.me.gvt1.com`
  - `safebrowsing.googleapis.com`、`sb-ssl.google.com`
  - `optimizationguide-pa.googleapis.com`、`content-autofill.googleapis.com`、`android.clients.google.com`
- `askUser` 一开头判断：目标正好是表里的域名（只比完整域名，不比后缀），就不发 `network/requestApproval`，直接按拒绝处理。
  - 照常写审计：`Event: "network_decision"`，`Actor: "program"`，`Decision: "deny_background"`，`Detail: "Chrome 的后台联网，不询问"`。
  - 打一行 `Network decision [<call_id>]: domain=<域名> decision=deny_background`。
  - `full` 模式也一样拒绝：这些请求在任何模式下都用不上。
  - `request_network_access`（模型自己申请）和代理共用 `askUser`，一并适用。
- 不进表的：`www.google.com`、`www.gstatic.com`、`accounts.google.com`、`cdn.jsdelivr.net` 这类正常任务可能用到的域名，照旧问人。有了 2.1 的参数，Chrome 不该再自己去连它们。

### 2.3 模块缓存目录不存在时不挂

- `/opt/gomod` 那一处的 `--ro-bind` 换成 `--ro-bind-try`（bwrap 的 `--ro-bind-try`：源目录不存在就跳过）。`GOMODCACHE=/opt/gomod` 照设。没有依赖的项目照常 `go build`；要用模块的项目，报错会说模块找不到（离线本来就下载不了），不会整个沙箱起不来。
- `/opt/go`（GOROOT）还用 `--ro-bind`：`go env` 能给出 GOROOT，它就一定存在。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/sandbox/sandbox.go` | 2.1 的包装脚本（生成内容的函数、`--perms`/`--ro-bind-data`、`ExtraFiles` 第二项、`PATH`）；bash 工具说明补那一句；2.3 的 `--ro-bind-try` |
| `internal/sandbox/network.go` | 2.2 的 `backgroundHosts` 和 `askUser` 开头的判断 |
| `internal/sandbox/AGENTS.md`（有的话；没有就不建） | 补一条：沙箱里的 `chrome` 是包装过的，后台联网固定关掉；表里的 Chrome 后台域名直接拒绝 |

不改：联网代理的端口、IP、内网检查（`dialChecked`）；白名单和审批的流程；浏览器工具（`internal/browser`，它在沙箱外跑，有自己的启动参数）。

## 4. 这一期不做

- 按进程区分“是 Chrome 发的”（代理不知道是哪个进程连的）。
- 让用户自己配“直接拒绝”的域名表。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过。
2. 读代码报告：包装脚本的内容怎么生成（Chrome 在 `/opt/chrome` 和在 `/usr` 两种）、fd 编号、`PATH` 的顺序；`askUser` 里直接拒绝的判断在写审计和发请求的前后关系。

（沙箱里起不了嵌套的沙箱，2.1–2.3 的实际效果由 Claude Code 在沙箱外验。）

### Claude Code 在沙箱外做（真实模型，临时 `HOME`，网页端 8091）

3. 新对话让模型用说明里的命令加载一个本地 HTML，并打印 DOM：这一轮的存档里没有联网审批（`approval` 事件），输出里有 DOM。
4. 让模型跑 `curl -sS -m 10 https://update.googleapis.com/ ; echo rc=$?`：很快失败，存档里没有联网审批，审计里有一条 `deny_background`。
5. 让模型跑 `curl -sS -m 10 https://www.google.com/ ; echo rc=$?`：照旧弹联网审批（拒绝即可）。
6. 临时 `HOME` 下没有 `go/pkg/mod`，选一个 Go 仓库当工作区，让模型跑 `go version && ls`：沙箱正常起来，有输出。
