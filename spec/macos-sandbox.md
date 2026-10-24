# macOS 上的 bash 沙箱（Seatbelt）

状态：设计（2026-10-24），交给 tern 实现（codex-parity-plan.md 6.1）。本机是 Linux，macOS 上的实际效果由用户在 Mac 上测。

## 1. 为什么要改

bash 沙箱只支持 Linux 的 bubblewrap：`internal/sandbox/sandbox.go:280` 在别的系统上直接报“沙箱只支持 Linux（bubblewrap）”，设置里 bash 显示“Bash 沙箱只支持 Linux”（`internal/appserver/settings.go` 的 `unavailable`）。用户的主力机是 Mac，在 Mac 上直接跑 tern 就没有 bash，现在只能绕道 Lima 虚拟机。

Codex 在 macOS 上用 Seatbelt（`sandbox-exec` 加一段策略），在 Linux 上用 Landlock 和 bwrap。

## 2. 改什么

### 2.1 怎么跑

- macOS 上用 `/usr/bin/sandbox-exec -p <策略> /bin/bash -c <命令>` 代替 bwrap。没有 `/usr/bin/sandbox-exec` 时照旧报不可用。
- macOS 没有挂载命名空间，命令看到的是**真实路径**，不是 `/workspace`、`/scratch`：
  - 工作目录：有工作区时是工作区的真实路径，没有时是这条对话的沙箱目录（`sandbox.Dir`）；
  - 告诉模型 bash 里的路径：`workspace.BashMount` 设成工作区的真实路径（环境信息里那一项“bash 里的工作区”本来就是为这种情况准备的）；
  - bash 工具的说明（`Rules()`）里 `/workspace`、`/scratch` 换成真实路径。
- 环境变量照 Linux 的做法清空、只留需要的（`PATH`、`HOME` 设成沙箱目录、`LANG`、`TMPDIR`、代理），密钥不带进去。

### 2.2 策略（SBPL）

照 Codex 的 Seatbelt 基础策略的思路，写成 `internal/sandbox/seatbelt.sbpl`，用 `//go:embed` 编进程序，运行时用 `-D` 参数传路径：

```scheme
(version 1)
(deny default)
; 进程、信号、sysctl、mach 等运行 shell 和常用工具需要的
(allow process-exec process-fork signal (target same-sandbox))
(allow sysctl-read mach-lookup ipc-posix-shm file-ioctl pseudo-tty)
; 读：整个文件系统，除了几处
(allow file-read*)
(deny file-read* (subpath (param "TERN_DATA")) (literal (param "TERN_ENV")))
(deny file-read* (subpath (param "PROJECT_DATA")) (literal (param "PROJECT_ENV")))
; 写：沙箱目录、临时目录，可写时加工作区（工作区的 .git 只读）
(allow file-write* (subpath (param "SCRATCH")) (subpath (param "TMP")) (literal "/dev/null"))
; WRITABLE 为 1 时（权限模式不是 plan）：
(allow file-write* (subpath (param "WORKSPACE")))
(deny file-write* (subpath (param "WORKSPACE_GIT")))
; 网络：只能连本机的联网代理
(allow network-outbound (remote tcp (param "PROXY")))
```

- 上面是意思，写法以 macOS 实际接受的为准：比如 `WRITABLE` 的分支在 Go 里拼两份策略，`PROXY` 写成 `localhost:<端口>`。
- `TERN_DATA` 是 `$TERN_HOME/.data`，`TERN_ENV` 是 `$TERN_HOME/.env`，`PROJECT_DATA`、`PROJECT_ENV` 是工作区里的 `.data`、`.env`，和 Linux 上用 tmpfs 盖掉的那几样一致。

### 2.3 联网

- Linux 上沙箱有自己的网络命名空间，联网代理的端口由 `sandbox-init` 转进去；macOS 没有，命令直接连 agent 在宿主机上监听的代理。
- 代理监听 `127.0.0.1` 的随机端口，`HTTP_PROXY` 和策略里的 `PROXY` 用这个端口。
- 审批、白名单、内网检查照旧（都在代理里）。

### 2.4 没有的东西（写进说明和 README）

- 没有 cgroup：内存、CPU、进程数不限制；时限照旧（超时杀掉进程组）。
- 没有 seccomp。
- 包装过的 Chrome（sandbox-quiet-chrome.md）只在 Linux 上有；macOS 的沙箱说明里不提 Chrome。
- 后台命令（`jobs.go`）照旧能用，停止时杀进程组。

### 2.5 代码怎么分

- 和平台有关的部分拆成 `launch_linux.go`（现在的 bwrap、systemd-run、seccomp）和 `launch_darwin.go`（sandbox-exec）；`sandbox.go` 里留共用的（参数解析、输出截断、后台命令、规则文字）。
- `Run` 里的 `runtime.GOOS != "linux"` 改成：既不是 Linux 也不是 macOS 才报不可用。
- `unavailable("bash")`：macOS 上有 `/usr/bin/sandbox-exec` 就可用。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/sandbox/sandbox.go` | 拆出平台相关的部分；`Run`、`Rules()` 按平台 |
| `internal/sandbox/launch_linux.go`（新文件，从 `sandbox.go` 搬） | bwrap 的那部分，行为不变 |
| `internal/sandbox/launch_darwin.go`（新文件） | sandbox-exec 的启动、策略参数、代理端口 |
| `internal/sandbox/seatbelt.sbpl`（新文件） | 策略 |
| `internal/sandbox/network.go` | macOS 上代理监听随机端口并告诉启动部分 |
| `main.go` | `workspace.BashMount` 在 macOS 上是真实路径（`main.go:694`） |
| `internal/appserver/settings.go` | `unavailable("bash")` 认 macOS |
| `internal/sandbox/AGENTS.md` | 补一条：macOS 用 Seatbelt，没有 cgroup 和 seccomp |
| `README.md` | 工具配置里 Bash 那一行：Linux 用 bubblewrap，macOS 用 sandbox-exec（Seatbelt），macOS 上不限制内存与 CPU |

不改：Linux 上的行为（拆文件只是搬代码，验收要确认 Linux 照旧）。

## 4. 这一期不做

- Windows。
- macOS 上的资源限制。
- macOS 上包装 Chrome。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`GOOS=darwin GOARCH=arm64 go build ./...` 和 `GOOS=darwin go vet ./...` 也通过。
2. 读代码报告：拆文件后 Linux 的启动参数和之前一字不差（把 `bwrapArgs` 改动前后的输出对比一次）；macOS 上策略参数怎么填。

### Claude Code 在沙箱外做（真实模型，本机 Linux）

3. Linux 照旧：跑一条用 bash 的对话（`ls && go version`），结果正常；用包装过的 Chrome 加载页面没有联网审批。

### 用户自己测（Mac 上）

4. 在 Mac 上 `go install .`，`tern -bash -workspace <某个仓库> -question '运行 pwd && ls && cat /etc/hosts | head -3'`：能跑，`pwd` 是仓库的真实路径。
5. `-question '运行 touch ~/x.txt'`：失败（不能写家目录）；`touch <仓库>/x.txt` 在 `edit` 模式下成功、`plan` 模式下失败。
6. `-question '运行 cat <TERN_HOME>/.env'`：读不到。
7. `-question '运行 curl -sS https://example.com -o /dev/null'`：弹联网审批（网页端）或在命令行里按不可用拒绝。
