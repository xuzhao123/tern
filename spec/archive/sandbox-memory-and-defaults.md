# 沙箱内存、命令行默认值与代理地址变量

状态：已实现（2026-10-23，tern 用 DeepSeek 实现；验收第 2 条里的 darwin 编译要等 macOS 编译修复合并，之后补测通过，但当时缓存已热）。

## 1. 为什么要改

2026-10-23 验收 `spec/bash-timeout-and-polling.md` 时又遇到三个问题：

1. **沙箱内存 512MB 不够。**沙箱里冷缓存编译整个模块时被 OOM 杀掉（系统日志：`The kernel OOM killer killed some processes in this unit`，内存峰值 512M，被杀的是 `compile`）。systemd 随后结束整个 scope，模型看到的只有 `"signal": "terminated"`，不知道是内存不够。实测：沙箱只给 1 个 CPU，Go 却按 4 核同时起几个编译进程；限成 1 个并行（`-p=1`）冷编译从 63 秒变成 54 秒，同时在跑的编译进程更少。
2. **命令行的两个默认值是学习时期的值。**`-max-steps` 默认 10、`-context-window` 默认 12288 token：编码任务两轮工具结果就会触发压缩失败（`context_error`）。网页端早就按模型取窗口（取不到时 262144）、默认不限请求次数。
3. **`LLM_API_URL` 一个变量两用。**它是方舟的地址变量，又被网页端拿来当“经代理”的覆盖地址，而且对所有供应商生效。把 `.env` 导出成环境变量后，选 DeepSeek 时请求会带着 DeepSeek 的密钥发到方舟的地址，报“密钥格式不对”，日志里显示的上游却还是 DeepSeek，很难查。

## 2. 改什么

### 改动一：沙箱内存 2G，Go 编译一次一个包，被系统结束时说清楚

- `memoryMax` 从 `512M` 改成 `2G`（机器 7.7G 内存；前台加后台最多同时 5 个沙箱，这是上限，不是预留）。
- 挂了 Go 工具链时，沙箱里的 `GOFLAGS` 从 `-mod=readonly` 改成 `-mod=readonly -p=1`：沙箱只有 1 个 CPU（`CPUQuota=100%`），并行编译只会多占内存。
- 命令不是被我们终止的（不是时限到、不是被停止），却是被信号结束的：`SIGKILL` 和 `SIGTERM` 都给出同一句说明：进程被系统结束，多半是超出了内存上限（写明上限的值）；Go 项目可以分开编译各个包，其他程序减少并行。现在只有 `SIGKILL` 有说明。

### 改动二：命令行默认值和网页端一致

- `-max-steps` 默认从 10 改成 0（不限）。防止无限循环靠重复动作与无进展熔断，以及人按 Ctrl-C。
- `-context-window` 默认从 12288 改成 0，0 表示按模型：`cmp.Or(llm.WindowOf(provider, model), 262144)`，和网页端 `startRun` 里的写法一致。显式给值时照旧用给的值。
- 队列和评测没有人在旁边看着，保持现在的预算：`queue.RunTask`（队列与评测共用）在任务参数里没有 `-max-steps` 时补上 `-max-steps 10`。上下文窗口跟着模型走，不再固定 12288。

### 改动三：代理地址用自己的变量

- 网页端启动 agent（`server.go` 的 `launch`）和在网页端里跑评测（`eval.go`）时，代理地址改放在新变量 `AGENT_PROXY_URL` 里，不再写 `LLM_API_URL`。
- `llm.LoadConfig`：`AGENT_PROXY_URL` 不为空时，请求发往它（现在是读 `LLM_API_URL`）；方舟那段“被代理覆盖时把上游还原”的特殊处理一起删掉。`LLM_API_URL` 从此只是方舟的地址变量。
- 子 agent 继承进程环境，`AGENT_PROXY_URL` 照样传下去，不用另外处理。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/sandbox/sandbox.go` | `memoryMax` 改成 `"2G"`；`bwrapArgs` 里 `GOFLAGS=-mod=readonly` 改成 `GOFLAGS=-mod=readonly -p=1`；`execute` 里信号结束的分支：`SIGKILL` 或 `SIGTERM` 都写 `note`，内容按第 2 节；资源上限那段注释同步改 |
| `main.go` | `-max-steps` 默认值改成 0，帮助文字写明 0 是不限；`-context-window` 默认值改成 0，帮助文字写明 0 是按模型；在构造 `agent.ContextOptions` 之前，`*window` 为 0 时按 `cmp.Or(llm.WindowOf(*provider, *model), 262144)` 取值（后面的 `options.Capacity()` 校验照旧） |
| `internal/queue/queue.go` | `RunTask` 拼好 `args` 后，如果里面没有 `-max-steps`（`-max-steps`、`-max-steps=…` 两种写法都算），在前面补 `"-max-steps", "10"`；注释说明为什么 |
| `internal/web/server.go` | `launch` 里 `"LLM_API_URL=http://"+s.addr+"/llm/"+r.ID` 改成 `"AGENT_PROXY_URL=…"` |
| `internal/web/eval.go` | 评测子进程的 `Env` 同样改成 `AGENT_PROXY_URL` |
| `internal/llm/protocol.go` | `LoadConfig` 读 `AGENT_PROXY_URL`；删掉 `p.URLVar == "LLM_API_URL"` 那段；函数上面的注释改成新变量 |
| `README.md` | 参数表里 `-max-steps`、`-context-window` 的默认值与说明（常用参数表和完整参数表两处都有） |

不要改的：网页端的设置与页面；`llm.go` 里判断“经本机代理”的条件（`http://127.0.0.1`），远程 agent 设计里另有安排；子 agent 的请求预算 `-subagent-steps`。

## 4. 这一期不做

- 内存、CPU 上限做成参数。
- 只读模块缓存引起的 `go: writing stat cache … read-only file system` 提示。
- 网页端的设置项。

## 5. 验收

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过。
2. 内存：删掉 `.data/gocache` 后，`./bin/tern -workspace <一份 clone> -bash -check 'go build -o /dev/null ./... && GOOS=darwin GOARCH=arm64 go build -o /dev/null ./... && echo OK'` 输出 OK；同一时段 `journalctl --user` 里这个 scope 没有 `oom-kill`。
3. OOM 说明：`./bin/tern -bash -check "python3 -c 'x = bytearray(3 * 1024**3)'"` 的结果里有内存上限的说明（`note` 提到 2G）。
4. 命令行默认值：`./bin/tern -provider deepseek -question '只回答：好'` 的日志里 `Limits:` 一行是 `max_steps=不限`，`Context:` 一行的 `cap=` 接近 deepseek-flash 的 1048576，而不是 12288 一级。
5. 队列预算：用一个只有一道题的任务文件跑 `./bin/tern queue <文件>`，这个任务检查点的 `args` 里有 `-max-steps 10`。
6. 代理变量：在导出了整个 `.env`（含 `LLM_API_URL`）的 shell 里运行 `./bin/tern -provider deepseek -question '只回答：好'`，请求成功；网页端发起一次对话也正常（模型请求经代理，存档里有请求记录）。
