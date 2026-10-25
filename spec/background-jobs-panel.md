# 网页端看、停后台命令

状态：已实现并完成收尾验证（2026-10-25），范围与证据见 [本批收尾记录](closeout-20261025.md)。

## 1. 为什么要改

模型用 `bash` 带 `background=true` 起的后台命令（`internal/sandbox/jobs.go`，最多 4 个，只活到这一轮结束），只有模型自己能用 `bash_output` 看、用 `bash_kill` 停。人在页面上不知道后台跑着什么，一个卡住的构建只能等这一轮超时，或者停掉整轮。

Codex 的 app-server 有 `thread/backgroundTerminals/list`、`terminate`、`clean`，TUI 里是 `/ps`、`/stop`。

## 2. 改什么

### 2.1 agent：报告后台命令的变化

- 后台命令启动、自己结束、被停掉时（`startJob`、`job` 结束、`stop`），发一条通知 `job/updated`：
  ```json
  {"id": "job-1", "command": "go test ./...", "state": "running|exited|stopped",
   "exit_code": 0, "started_at": "…", "ended_at": "…", "stopped": "原因"}
  ```
  `command` 最多 200 个字。app-server 照现有的写法把 agent 的通知原样记进存档（`internal/appserver/live.go:203`）。
- agent 的协议处理（`main.go` 里 `turn/steer`、`turn/interrupt` 那一组）加两个方法：
  - `job/output {id}`：返回 `{output, dropped, state}`，是这个后台命令**最近**的 8KB 输出。只看不消费：不动给模型看的读取位置（`job.read`），模型下次 `bash_output` 照样能读到。
  - `job/kill {id}`：照 `bash_kill` 停掉，`stopped` 写“用户在网页上停掉”。

### 2.2 app-server

- `thread/job/output {threadId, jobId}`、`thread/job/kill {threadId, jobId}`：对话在跑才转给 agent（`r.conn.Call`），没在跑回 409“这一轮已经结束，后台命令也一起结束了”。
- 停掉的写审计：`Event: "user_job_kill"`，`Target` 是命令。

### 2.3 网页端

- 顶栏按钮旁边加“后台命令（N）”（`#jobs-btn`）。N 是这条对话最新一轮里还在跑的数目；这一轮有过后台命令才显示。点开是侧栏（和沙箱文件、改动面板互斥）。
- 侧栏列出这一轮的后台命令（从存档里的 `job/updated` 通知攒起来）：命令、状态（在跑 / 已结束，退出码 N / 已停止：原因）、跑了多久。
  - 在跑的有“看输出”和“停止”。“看输出”调 `thread/job/output`，在下面显示（`textContent`），每 2 秒刷新一次，直到它结束或人收起；“停止”先 `confirm()`。
  - 这一轮结束以后，列表还在（只读），没有按钮。
- 运行中的对话页面上，后台命令的数目变化跟着 `job/updated` 刷新。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/sandbox/jobs.go` | 三处发 `job/updated`；“只看不消费”的读最近输出的函数；给 `job/kill` 用的停止入口 |
| `main.go` | agent 协议处理加 `job/output`、`job/kill` |
| `internal/appserver/server.go` | `thread/job/output`、`thread/job/kill` |
| `internal/appserver/handle.go` | 方法表加两项 |
| `internal/web/server.go` | `GET /runs/{id}/jobs/{jid}/output`、`POST /runs/{id}/jobs/{jid}/kill` |
| `internal/web/page/runs.js` | 从 `job/updated` 攒出这一轮的后台命令列表 |
| `internal/web/page/jobs.js`（新文件） | 侧栏 |
| `internal/web/page/agents.js` | 侧栏的分派加这一个（`ui.jobsOpen`） |
| `internal/web/page/index.html` | `#jobs-btn` |
| `internal/web/page/shell.js` | 按钮的显示与数目 |
| `internal/web/page/app.css` | 样式 |
| `README.md` | 网页端那段补一句：顶栏“后台命令”看模型在后台跑的命令，可以看输出、停掉 |

不改：后台命令的上限、只活到这一轮结束的规则、`bash_output`、`bash_kill`。

## 4. 这一期不做

- 一轮结束后保留后台命令继续跑。
- 人从页面上起后台命令。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过改过和新加的页面脚本。
2. 读代码报告：“只看不消费”的实现（不动 `job.read`）；三处 `job/updated` 的位置。

### Claude Code 在沙箱外做（真实模型、无头 Chrome，临时 `HOME`）

3. 让模型用后台方式跑 `for i in $(seq 1 60); do echo $i; sleep 1; done`，再让它等一会儿：顶栏出现“后台命令（1）”；侧栏“看输出”能看到数字在涨；模型之后调 `bash_output` 仍然拿到完整输出。
4. 点“停止”：状态变“已停止：用户在网页上停掉”；审计里有 `user_job_kill`；模型下一次 `bash_output` 看到它被停了。
5. 这一轮结束后：列表还在，没有按钮；`POST …/kill` 回 409。
