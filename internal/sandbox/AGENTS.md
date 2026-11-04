# sandbox 的约束

- bubblewrap + seccomp（纯 Go 生成 BPF）+ systemd-run cgroup，不用 Docker。每次 bash 调用一个新沙箱，后台命令也是：后台起的服务，之后的命令连不上（同 Claude Code 在 Linux 上的沙箱）。后台命令只活到这一轮结束。
- macOS 用 Seatbelt（`launch_darwin.go` 调 `/usr/bin/sandbox-exec -p`，策略在 `seatbelt.sbpl`），没有 cgroup 与 seccomp：内存、CPU、进程数不限制，时限照旧（超时杀进程组）。没有挂载命名空间，命令看到的是真实路径：工作目录是工作区的真实目录（没选工作区时是本对话的沙箱目录），`HOME` 是本对话的沙箱目录。平台相关的启动在 `launch_linux.go`、`launch_darwin.go`，`sandbox.go` 里只放共用的。
- bash 的工作目录跨轮保留（同 Claude Code）：命令包一层 cd 与 EXIT trap，pwd 写进本对话目录的 `.config/tern/cwd`（`cwd.go`），agent 存进检查点 state 的 `cwd`、`cwd_host`；离开工作区和本对话目录就重置回根目录，换了宿主机/沙箱模式也从根目录开始。环境变量不保留。
- 工作区挂到 `/workspace`（plan 模式只读；`.git` 只读，不是 git 仓库时也占一个空的只读目录，命令造不出来；`.env` 换空、`.data` 换空目录），本对话目录挂到 `/scratch`。环境变量全部清空。沙箱目录里预先写 `.config/go/telemetry/mode` 为 `off`（Go 只认这个 mode 文件，不认 `GOTELEMETRY`），已有就不动。
- 沙箱里的 `chrome` 命令是包装过的（`/opt/chrome-bin` 下的同名脚本，在 `PATH` 最前面），Chrome 自己的后台联网固定关掉；`network.go` 的 `backgroundHosts` 表里的 Chrome 后台域名直接拒绝、不问人。
- 默认断网，只经 agent 进程里的白名单代理出去；白名单只能由用户添加（`-net-allow`、网页端设置 `net_allow` 或当场审批），full 模式由程序放行、写审计，端口、IP 与内网地址照常拒绝。浏览器与沙箱代理的出口判断共用 `internal/netguard`。
- `-bash-host`（完全访问）时普通 bash 与 `!命令` 直接在宿主机上跑：不建 namespace、不装 seccomp、不经代理、不加 cgroup，继承宿主机环境但去掉 `AGENT_*` 运行时凭据；工作目录是有工作区时的真实路径。`Check()`（`-check`、`verify_feature`）始终进沙箱，不受它影响。`full` 权限不自动开它。
