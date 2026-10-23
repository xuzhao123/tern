# sandbox 的约束

- bubblewrap + seccomp（纯 Go 生成 BPF）+ systemd-run cgroup，不用 Docker。每次 bash 调用一个新沙箱，后台命令也是：后台起的服务，之后的命令连不上（同 Claude Code 在 Linux 上的沙箱）。后台命令只活到这一轮结束。
- 工作区挂到 `/workspace`（plan 模式只读；`.git` 只读、`.env` 换空、`.data` 换空目录），本对话目录挂到 `/scratch`。环境变量全部清空。
- 默认断网，只经 agent 进程里的白名单代理出去；白名单只能由用户添加（`-net-allow`、网页端设置 `net_allow` 或当场审批），full 模式由程序放行、写审计，端口、IP 与内网地址照常拒绝。浏览器与沙箱代理的出口判断共用 `internal/netguard`。
