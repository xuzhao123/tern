# MCP 资源：列出、读取 server 提供的 resources

状态：已实现并完成收尾验证（2026-10-25），范围与证据见 [本批收尾记录](closeout-20261025.md)。

## 1. 为什么要改

tern 的 MCP 客户端只接 server 的 tools（`internal/mcp/mcp.go`），不接 resources。不少 server 把文档、数据库表结构、文件这类只读内容作为 resources 提供，tern 用不上。Codex 有 `list_mcp_resources`、`list_mcp_resource_templates`、`read_mcp_resource`（`codex-rs/core/src/tools/handlers/mcp_resource.rs`），配了 MCP server 才出现。

## 2. 改什么

- 连接时记下 server 的名字：`Connection` 加 `Name`（`initialize` 回复的 `serverInfo.name`；为空时用连接的序号 `server1`、`server2`…），以及它声明没声明 `resources` 能力。
- 只要有一个已连接的 server 声明了 `resources`，就加两个工具（只加一次，不按 server 加）：
  - `list_mcp_resources {server?}`：列出资源，`server` 不给就列全部。每项 `{server, uri, name, description, mimeType}`。分页由 tern 内部翻完（`ListResources` 的 cursor），每个 server 最多 200 项，多了注明截断。
  - `read_mcp_resource {server, uri}`：读一个资源（`ReadResource`）。文本内容原样返回；二进制只返回类型和大小（`{"mimeType": …, "bytes": N, "note": "二进制内容不放进上下文"}`）。结果照工具输出的预算截断，超出部分照常进 `read_output`。
- 两个工具在 `toolspec.go` 里标成只读、可重复。外发检查照 MCP 工具现在的规则：读到的内容是不可信的资料（`authority` 照 MCP 结果标）。
- 不做资源模板（`resources/templates/list`）和订阅（`resources/subscribe`）。
- 网页端的 MCP 中心“测试连接”（`hub/mcp/test`）的输出里，多列一行“resources：N 个”（server 声明了才列）。

## 3. 按文件的改动

| 文件 | 改动 |
| --- | --- |
| `internal/mcp/mcp.go` | `Connection` 加 `Name`、是否有 resources；两个工具的定义与实现 |
| `main.go` | 连完 MCP server 后，有 resources 就把两个工具加进 `llm.Tools` |
| `internal/agent/dispatch.go` | 分派这两个工具 |
| `internal/agent/toolspec.go` | 两个工具的特性 |
| `main.go` 里 `-mcp-list` 的输出 | 带上 resources 的数目（MCP 中心“测试连接”用的就是它） |
| `README.md` | MCP 那一行补：server 提供 resources 时多两个工具 `list_mcp_resources`、`read_mcp_resource` |

不改：MCP 工具的注册与命名、破坏性工具的确认、网页端内置的计算器 MCP（它不提供 resources）。

## 4. 这一期不做

- 资源模板、订阅、资源变更通知。
- prompts（MCP 的 `prompts/list`）。

## 5. 验收

### tern 自己做（不需要真实模型，在沙箱里）

1. `gofmt -l .` 没有输出；`go vet ./...`、`go build ./...` 通过。
2. 在仓库里建临时目录（验完删掉）写一个只用 mcp-go 的小 MCP server（stdio，提供两个文本资源、一个二进制资源），再写一个小程序用 `internal/mcp` 连上它，调两个工具的实现，报告结果：列出 3 项；读文本拿到原文；读二进制只有类型和大小。

### Claude Code 在沙箱外做（真实模型）

3. 用同一个小 server（编成二进制放在临时目录）作为 `-mcp-server`：`-question '列出 MCP 的资源，读一下其中的 readme'`，模型调用两个工具，回答里有资源的原文。
4. 只连计算器 MCP（不提供 resources）：工具清单里没有这两个工具。
