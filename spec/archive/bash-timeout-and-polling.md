# bash 时限与后台轮询

状态：已实现（2026-10-23，tern 用 DeepSeek 实现，验收见第 5 节）。

## 1. 为什么要改

2026-10-23 用 tern（DeepSeek）在网页端做一个编码任务（让 tern 能在 macOS 上编译），改动做对了，但这一轮没能正常结束：

1. 模型在 bash 里跑 `GOOS=darwin GOARCH=arm64 go build ./...`，**30 秒**被掐断（exit=-1）。实测沙箱里冷缓存编译一次要 63–64 秒（沙箱只给 1 个 CPU），30 秒必然不够。
2. 模型随后改用后台命令，用 `bash_output {"job_id":"job-2","wait":20}` 等结果。同样的调用出现第 3 次时，被“重复动作”检测当成死循环拦下（`repeated_action`），这一轮结束，后台编译也随之被终止。

等一个后台任务跑完，本来就要用同样的参数反复问；而真实项目的编译、测试常常超过 30 秒（Claude Code 的 Bash 默认时限是 2 分钟）。

## 2. 改什么

### 改动一：bash 有自己的时限，默认 2 分钟

- 新参数 `-bash-timeout`，默认 `2m`，范围大于 0、不超过 `10m`。它是 bash 单条命令的时限：到时沙箱终止整条命令，交回终止前的输出（现有行为不变，只是时限换了来源）。
- `-tool-timeout` 不变（默认 30s），只管其他工具。
- 子 agent 继承 `-bash-timeout`。
- 网页端这期不加设置项：网页端启动 agent 时不传这个参数，用默认的 2 分钟。

### 改动二：轮询后台任务不算重复动作

- 工具声明里加一个性质 `Poll`：这个工具是在查询后台任务的进度，用同样的参数反复调用是正常的。
- `bash_output`、`agent_wait` 声明 `Poll`。
- 重复动作检测（最近 6 个动作里同一工具与参数出现 3 次就熔断）跳过 `Poll` 的调用：既不计数，也不放进最近动作的窗口。其他检查照旧：调用ID、类型、名字的有效性，无进展检测，请求预算。
- 轮询仍然有边界：后台命令只活到这一轮结束；`bash_output` 每次最多等 20 秒；`agent_wait` 有自己的时限；整轮受 `-max-steps` 限制。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `main.go` | 在 `-tool-timeout` 旁边加 `-bash-timeout`（`time.Duration`，默认 `2*time.Minute`）；参数校验加上它的范围（大于 0、不超过 10 分钟），报错文字说明范围；`sandbox.Timeout = agent.ToolTimeout` 改成用 `-bash-timeout` 的值，旁边的注释同步改；子 agent 继承名单 `share` 里加 `"bash-timeout"`，并删掉已经不存在的 `"lab-tools"` |
| `internal/agent/toolspec.go` | `Spec` 加字段 `Poll bool`，注释写清含义；`bash_output`、`agent_wait` 的声明加 `Poll: true`；`specOf` 里 bash 的时限从 `ToolTimeout + sandbox.ApprovalWait + 30*time.Second` 改成 `sandbox.Timeout + sandbox.ApprovalWait + 30*time.Second` |
| `internal/agent/react.go` | 校验一批调用的循环里（`actionKey`、`pushAction` 那一段），`specOf(call.Function.Name).Poll` 为真时跳过计数与熔断；有效性检查仍对每个调用做。`Limits:` 那一行日志加上 `bash_timeout=%s` |
| `internal/agent/approval.go` | `replayProgress` 重建最近动作时同样跳过 `Poll` 的调用，和实时检测一致 |
| `README.md` | 参数表加 `-bash-timeout` 一行；`-tool-timeout` 那一行改成“bash 用 `-bash-timeout`” |
| `internal/sandbox/sandbox.go` | `Timeout` 变量上面的注释改成：它是 bash 单条命令的时限，由 main 按 `-bash-timeout` 设置（现在写的是“就是执行器的单次工具时限（-tool-timeout）”，已经不对） |

不要改的：沙箱里终止命令、交回输出的逻辑；`bash_output` 的等待上限（20 秒，不超过单条命令时限的一半）；无进展检测；网页端的设置项与页面。

## 4. 这一期不做

- 模型按条指定时限（Claude Code 的 Bash 有 `timeout` 参数）。
- 网页端的设置项。
- 改无进展检测。`bash_output` 的结果里带着已运行时长，每次都不一样，不会被它误判。

## 5. 验收

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过。
2. `go run . -h 2>&1 | grep -A1 bash-timeout` 显示这个参数，默认值是 2m0s。
3. 真实模型（DeepSeek），在一份新的 clone 里，先删掉沙箱的编译缓存 `.data/gocache` 造成冷缓存，任务是“在 bash 里前台运行 `GOOS=darwin GOARCH=arm64 go build ./...` 并告诉我结果”：命令一次跑完（约 1 分钟）、没有被终止，这一轮正常结束。
4. 真实模型，任务是“在后台运行 `sleep 70; echo done`，等它结束后告诉我输出”：模型用 `bash_output` 轮询到命令结束，这一轮正常结束，结局不是 `repeated_action`。
5. 子 agent 继承：以 `-bash-timeout 3m -subagents` 运行并让它派一个子 agent，子任务检查点的 `args` 里有 `-bash-timeout=3m0s`（只继承显式给出的参数；不给时父子都是默认的 2 分钟）。
