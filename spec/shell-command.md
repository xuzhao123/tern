# 在网页端跑一条命令：`!命令`

状态：已实现并完成收尾验证（2026-10-25），范围与证据见 [本批收尾记录](closeout-20261025.md)。

## 1. 为什么要改

想看一眼 `git status`、跑一下测试，现在只能让模型去跑（还要等它想一轮），或者回终端自己敲。Codex 的输入框里 `!命令` 直接跑，输出进对话，下一轮模型也看得到（协议里的 `thread/shellCommand`）。

## 2. 改什么

### 2.1 在哪里跑：和模型的 bash 同一个沙箱

不在宿主机上直接跑。照 tern 的安全约定，和模型的 bash 一样在沙箱里跑：
- 有工作区时，`/workspace` 是工作区。这条对话的权限模式是 `plan` 时只读，否则可写。
- `/scratch`（没有工作区时是 `/workspace`）是这条对话的沙箱目录（`.data/sandbox/<第一轮ID>`），和模型的 bash 看到的一样。
- 断网：联网只认白名单（这条对话快照里的 `net_allow`），不在白名单的直接拒绝，不问人（这时没有 agent 在跑，也就没有审批的通道）。
- 时限、内存和模型的 bash 一样。

和 Codex 不同：Codex 的 `!` 用的是用户自己的 shell。

### 2.2 agent：`-shell` 模式

- `main.go` 加 `-shell <命令>`，照 `-check`（`main.go:711`）的写法：
  - 不请求模型，只在沙箱里跑这一条，最后一行输出 JSON `{exit_code, output, truncated, duration_ms}`；
  - 要求 `-bash`；`-workspace` 可选。
- 和 `-check` 的区别：
  - 工作区按 `-permission-mode` 决定可写不可写（`-check` 一律只读）；
  - 沙箱目录用环境变量 `AGENT_SHELL_DIR` 指定（app-server 填这条对话的沙箱目录），不另建 `check-…` 目录；
  - 联网白名单照 `-net-allow` 传进来的。
- 用 bash 工具同一套执行函数（可写的那条路径），不另写一份。

### 2.3 app-server：`thread/shellCommand`

- 参数 `{threadId, command}`：
  - 命令去掉首尾空白后不能为空，最长 4096 字节；
  - 这条对话在运行时回 409“这一轮在跑，等它结束再跑命令”；
  - 评测的试次、导入的对话回 409。
- 按这条对话的快照起 agent：`agentCommand("-shell", 命令, "-bash", …)`，带上工作区、权限模式、`-net-allow`，环境变量 `AGENT_SHELL_DIR`。最多等 3 分钟。
- 先写审计（`Event: "user_shell_command"`，`Target` 是命令），写不进就不跑。
- 跑完往存档里加一条事件 `{"kind": "shell", "text": <命令>, "body": {exit_code, output, truncated, duration_ms}}`，输出最多留 16KB；广播 `thread/status/changed`。返回同一份结果。
- **下一轮告诉模型**：`r.notes()`（`server.go:1079`）把上一次提问之后的 `shell` 事件也算进去，每条一句：“用户在两轮之间自己在沙箱里跑了命令 `<命令>`（退出码 N），输出：<最多 2000 字>”。

### 2.4 网页端

- 输入框里以 `!` 开头的内容，在一条已经停下的对话里发送时，不当成问题，走 `POST /runs/<id>/shell {command}`（去掉开头的 `!`）。
  - 新对话（还没有对话 ID）里以 `!` 开头：`#hint` 提示“先发一句话开个对话，再用 ! 跑命令”，不发送。
  - 运行中以 `!` 开头：Enter 不插话，`#hint` 提示“这一轮在跑，等它结束再跑命令”。
- 对话里在两轮之间显示一张卡片：`$ <命令>`，下面是输出（`textContent`，等宽字体，超过 30 行折叠），右上角“退出码 N · 用时”。截断时注明。
- 输入框以 `!` 开头时，占位或提示行写“! 在这条对话的沙箱里跑一条命令，输出下一轮模型也看得到”。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `main.go` | `-shell` 参数和执行分支 |
| `internal/sandbox/sandbox.go` | 如果现有的执行函数只给 bash 工具用，导出一个 `-shell` 也能调的入口（可写、指定沙箱目录） |
| `internal/appserver/server.go` | `thread/shellCommand`；`shell` 事件；`notes()` 算上它 |
| `internal/appserver/handle.go` | 方法表加一项 |
| `internal/web/server.go` | `POST /runs/{id}/shell {command}` → `thread/shellCommand` |
| `internal/web/page/composer.js` | `!` 开头的发送分支与提示 |
| `internal/web/page/runs.js` | 认 `shell` 事件 |
| `internal/web/page/chat.js` | 命令卡片 |
| `internal/web/page/app.css` | 卡片样式 |
| `README.md` | 网页端那段补一句：对话停下时，输入框里 `!命令` 在这条对话的沙箱里跑一条命令，输出下一轮模型也看得到 |

不改：模型的 bash 工具、`-check`、审批的流程。

## 4. 这一期不做

- 运行中跑命令。
- 在宿主机上直接跑（不进沙箱）。
- 交互式命令（要输入的）。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过；`node --check` 过改过的页面脚本。
2. 读代码报告：`-shell` 和 `-check` 的区别在代码里落在哪几行；`notes()` 里 shell 事件的提醒文字。

### Claude Code 在沙箱外做（真实模型、无头 Chrome，临时 `HOME`）

3. 有工作区的对话停下后，输入 `!git status --short`：对话里出现命令卡片，输出和在宿主机上跑的一致；存档里有 `shell` 事件，审计里有 `user_shell_command`。
4. 接着问“我刚才跑的命令输出了什么？”：模型能复述（请求里有那条提醒）。
5. `!echo hi > /workspace/x.txt`：权限模式 `edit` 时工作区里多了 `x.txt`；换成 `plan` 的对话再跑，报只读。
6. `!curl -sS -m 5 https://example.com`：失败（不在白名单），没有弹联网审批。
7. 运行中输入 `!ls`：不发送，提示等它结束。
