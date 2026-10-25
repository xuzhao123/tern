# 把 tern 当 MCP server：`tern mcp-server`

状态：已实现并完成收尾验证（2026-10-25），范围与证据见 [本批收尾记录](closeout-20261025.md)。

## 1. 为什么要改

别的 agent 客户端（Claude Code、Codex、编辑器插件）想把一件事交给 tern 做，现在只能起 `tern -question` 子进程、自己解析给人看的输出。Codex 有 `codex mcp-server`：别的客户端把 Codex 当一个 MCP 工具调用。tern 现在的 `-mcp-serve` 只暴露一个计算器（`internal/mcp/mcp.go:405`），是演示 MCP 用的。

## 2. 改什么

### 2.1 子命令

- `tern mcp-server`：stdio 上的 MCP server，名字 `tern`，版本是程序的版本。
- 经本机的 app-server 开对话，和网页端、chat 一样：先 `appclient.Start(false)`，再 `appclient.Dial("tern mcp-server")`。对话进 app-server 的列表，网页端看得到，审批也在网页端答。
- `-remote <别名>`：经 ssh 连远端的 app-server（`DialRemote`），和 `tern web -remote` 一样。

### 2.2 工具

| 工具 | 参数 | 做什么 |
| --- | --- | --- |
| `tern_start` | `prompt`（必填），`workspace`（工作区名字，可选），`provider`、`model`、`permission`（可选，空就用设置里的默认值） | 开一条新对话，等这一轮结束，返回 `{thread_id, outcome, answer}` |
| `tern_reply` | `thread_id`、`prompt`（必填），`permission`（可选） | 在已有对话上续聊一轮，等它结束，返回同样的内容 |
| `tern_threads` | `limit`（默认 20） | 列出最近的对话：`{id, name, query, done, workspace, updated_at}` |

- **等结束**：订阅这条对话的事件（`thread/resume`），等到 `exit`。等的时候每 10 秒发一次 MCP 进度通知（`notifications/progress`，带“第 N 次模型请求”），调用方知道还活着。
- **审批**：这一轮遇到联网或工具审批时，进度通知里写“在等审批：去网页端答复（<主机>）”。MCP 调用方答不了审批，照常等审批超时或网页端有人答。
- **取消**：调用方取消这次工具调用时（MCP 的 cancel），对这条对话发 `turn/interrupt`。
- `answer` 是这一轮的最终回答；没有回答时（出错、被停）是空串，`outcome` 写结局。
- 工具描述写清楚：tern 是一个能读写工作区、跑命令的编码 agent，一轮可能跑很多分钟；工作区要先在 tern 的网页端登记。

### 2.3 计算器

- `-mcp-serve`（计算器）不变；网页端的 `/mcp` 不变。两者和 `tern mcp-server` 是不同的东西，README 里写清楚。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/mcpserver/mcpserver.go`（新包） | `tern mcp-server` 的实现：MCP server（mcp-go 的 `server`）、三个工具、连 app-server、等结束、进度、取消 |
| `main.go` | 分派 `mcp-server` 子命令 |
| `internal/mcpserver/AGENTS.md`（新文件） | 这个包的约束：它是 app-server 的客户端，不自己起 agent，不读 `.data/` |
| `README.md` | “其他入口”表加一行 `tern mcp-server [-remote 别名]`，以及怎么配进 Claude Code、Codex（MCP 配置里 command 写 `tern`、args 写 `["mcp-server"]`） |

不改：app-server 的协议（只用现有的方法）、`-mcp-serve`。

## 4. 这一期不做

- 经 HTTP 提供（只做 stdio）。
- 在 MCP 调用里答审批（MCP 的 elicitation）。
- 把 tern 的 skills、斜杠命令暴露成 MCP prompts。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过。
2. 读代码报告：等结束的写法（订阅与 `exit`）；取消时发 `turn/interrupt` 的位置。

### Claude Code 在沙箱外做（真实模型，临时 `HOME`）

3. 起临时环境的网页端，再用一个小脚本当 MCP 客户端（stdio）连 `tern mcp-server`：`tools/list` 里有三个工具。
4. `tern_start {prompt: "1+1 等于几", provider: "deepseek"}`：返回 `thread_id` 和回答 2；网页端列表里多了这条对话。
5. `tern_reply` 在这条对话上问“再加 1 呢”：回答 3。
6. 调用 `tern_start` 跑 `sleep 30` 的任务，中途发 cancel：这条对话被停下。
### 用户自己测

7. 在自己的 Claude Code 或 Codex 里配上（例如 `claude mcp add tern -- tern mcp-server`），让它“用 tern 算一下 2+3”：能调通，网页端里看得到这条对话。
