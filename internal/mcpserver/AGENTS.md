# mcpserver 的约束

- `tern mcp-server` 在 stdio 上提供 MCP（mcp-go 的 `server`）：别的 agent 客户端（Claude Code、Codex）把一件事交给 tern 做。
- 它是 app-server 的客户端，和网页端、chat 一样：不自己启动 agent，也不读写 `.data/` 里的对话与设置。对话经 `internal/appclient` 开在 app-server 里（`-remote` 时经 ssh 连远端），所以对话进 app-server 的列表，审批也在网页端答。
- 只用 app-server 现有的方法（`thread/start`、`turn/start`、`turn/interrupt`、`thread/list`、`thread/resume`），不改协议。
- stdout 只有 MCP 协议，日志写 stderr；`appclient.Start` 不往 stdout 打印，起 app-server 的输出在它自己的日志里。
- 与另外两样 MCP 不是一回事：`-mcp-serve` 是给别的客户端调的计算器（`internal/mcp`），网页端的 `/mcp` 是远端 agent 用的内置 server。
